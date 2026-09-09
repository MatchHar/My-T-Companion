package friendtogether

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math/big"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

var rawURL = base64.RawURLEncoding

func randomSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return rawURL.EncodeToString(b), nil
}
func randomID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	b[6] = (b[6] & 15) | 64
	b[8] = (b[8] & 63) | 128
	x := hex.EncodeToString(b)
	return x[:8] + "-" + x[8:12] + "-" + x[12:16] + "-" + x[16:20] + "-" + x[20:], nil
}
func hashSecret(value string) string {
	h := sha256.Sum256([]byte(value))
	return rawURL.EncodeToString(h[:])
}
func sameHash(a, b string) bool {
	return a != "" && len(a) == len(b) && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
func validSecret(s string) bool {
	b, e := rawURL.DecodeString(s)
	return e == nil && len(b) == 32 && rawURL.EncodeToString(b) == s
}
func canonicalID(id string) bool { v, ok := normalizeID(id); return ok && v == id }
func publicKey(k PublicKey) (*ecdsa.PublicKey, error) {
	if k.KTY != "EC" || k.CRV != "P-256" {
		return nil, ErrInvalid
	}
	x, e := rawURL.DecodeString(k.X)
	if e != nil || len(x) != 32 || rawURL.EncodeToString(x) != k.X {
		return nil, ErrInvalid
	}
	y, e := rawURL.DecodeString(k.Y)
	if e != nil || len(y) != 32 || rawURL.EncodeToString(y) != k.Y {
		return nil, ErrInvalid
	}
	key := &ecdsa.PublicKey{Curve: elliptic.P256(), X: new(big.Int).SetBytes(x), Y: new(big.Int).SetBytes(y)}
	if !key.Curve.IsOnCurve(key.X, key.Y) {
		return nil, ErrInvalid
	}
	return key, nil
}
func thumbprint(k PublicKey) string {
	// RFC7638 mandatory members only, lexicographic ordering, no whitespace.
	body, err := json.Marshal(struct {
		Crv string `json:"crv"`
		Kty string `json:"kty"`
		X   string `json:"x"`
		Y   string `json:"y"`
	}{Crv: "P-256", Kty: "EC", X: k.X, Y: k.Y})
	if err != nil {
		return hashSecret("")
	}
	return hashSecret(string(body))
}
func validDevice(d Device) bool {
	_, err := publicKey(d.PublicKey)
	return canonicalID(d.RecipientID) && err == nil
}
func validMember(m Member) bool {
	if !validDisplay(m.Alias) {
		return false
	}
	switch m.Model {
	case "generic", "model_s", "model_3", "model_x", "model_y", "cybertruck":
		return true
	}
	return false
}
func validDisplay(s string) bool {
	if len(s) > 256 || !utf8.ValidString(s) || utf8.RuneCountInString(s) > 64 || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) || r == 0x202a || r == 0x202b || r == 0x202c || r == 0x202d || r == 0x202e || r >= 0x2066 && r <= 0x2069 {
			return false
		}
	}
	return true
}
func validateOrigin(origin string, allowLoopback bool) (*url.URL, error) {
	if len(origin) > 512 {
		return nil, ErrInvalid
	}
	u, e := url.Parse(origin)
	if e != nil || u == nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || u.Path != "" || u.RawPath != "" || u.Opaque != "" || u.String() != origin {
		return nil, ErrInvalid
	}
	if u.Scheme != "https" {
		ip := net.ParseIP(u.Hostname())
		if !allowLoopback || u.Scheme != "http" || ip == nil || !ip.IsLoopback() {
			return nil, ErrInvalid
		}
	}
	if strings.ToLower(u.Host) != u.Host || strings.ContainsAny(u.Host, "\\\r\n\t ") || strings.HasSuffix(u.Host, ":") {
		return nil, ErrInvalid
	}
	if u.Scheme == "https" {
		host := u.Hostname()
		if net.ParseIP(host) != nil || !strings.Contains(host, ".") || strings.HasSuffix(host, ".") {
			return nil, ErrInvalid
		}
		for _, suffix := range []string{".local", ".localhost", ".internal", ".home", ".test", ".invalid"} {
			if strings.HasSuffix(host, suffix) {
				return nil, ErrInvalid
			}
		}
		labels := strings.Split(host, ".")
		for _, label := range labels {
			if label == "" || len(label) > 63 || strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
				return nil, ErrInvalid
			}
			for _, r := range label {
				if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-') {
					return nil, ErrInvalid
				}
			}
		}
		alpha := false
		for _, r := range labels[len(labels)-1] {
			alpha = alpha || r >= 'a' && r <= 'z'
		}
		if !alpha {
			return nil, ErrInvalid
		}
	}
	if port := u.Port(); port != "" && port != "443" && u.Scheme == "https" {
		return nil, ErrInvalid
	}
	if u.Scheme == "https" && u.Port() == "443" {
		u.Host = u.Hostname()
	}
	return u, nil
}

// Strict decoding rejects unknown fields, trailing values and duplicate keys at
// every nesting level. A duplicate signed field must never have two meanings.
func strictJSON(data []byte, v any) error {
	if len(data) == 0 || len(data) > 16<<10 {
		return ErrInvalid
	}
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return ErrInvalid
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if err := scanValue(d, 0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return ErrInvalid
	}
	// Foundation UUID Codable emits uppercase. Normalize UUID-valued identity
	// members without altering strings such as alias/origin or signed bytes.
	var raw any
	d = json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	if d.Decode(&raw) != nil {
		return ErrInvalid
	}
	normalizeJSONIDs(raw)
	normalized, err := json.Marshal(raw)
	if err != nil {
		return ErrInvalid
	}
	d = json.NewDecoder(bytes.NewReader(normalized))
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil {
		return ErrInvalid
	}
	return nil
}
func normalizeJSONIDs(v any) {
	switch object := v.(type) {
	case map[string]any:
		for key, value := range object {
			if key == "jti" || strings.HasSuffix(key, "_id") {
				if str, ok := value.(string); ok {
					if normalized, valid := normalizeID(str); valid {
						object[key] = normalized
					}
				}
			}
			normalizeJSONIDs(value)
		}
	case []any:
		for _, value := range object {
			normalizeJSONIDs(value)
		}
	}
}
func scanValue(d *json.Decoder, depth int) error {
	if depth > 16 {
		return ErrInvalid
	}
	t, err := d.Token()
	if err != nil {
		return ErrInvalid
	}
	delim, ok := t.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return ErrInvalid
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return ErrInvalid
			}
			seen[s] = true
			if err := scanValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim('}') {
			return ErrInvalid
		}
	case '[':
		for d.More() {
			if err := scanValue(d, depth+1); err != nil {
				return err
			}
		}
		end, err := d.Token()
		if err != nil || end != json.Delim(']') {
			return ErrInvalid
		}
	default:
		return ErrInvalid
	}
	return nil
}
func decodeBody(w http.ResponseWriter, r *http.Request, v any) error {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if len(r.Header.Values("Content-Type")) != 1 || err != nil || mediaType != "application/json" {
		return ErrInvalid
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	defer r.Body.Close()
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return ErrInvalid
	}
	return strictJSON(b, v)
}
func emptyBody(w http.ResponseWriter, r *http.Request) error {
	var v struct{}
	return decodeBody(w, r, &v)
}

type proofHeader struct {
	Type      string    `json:"typ"`
	Algorithm string    `json:"alg"`
	JWK       PublicKey `json:"jwk"`
}
type proofClaims struct {
	JTI   string `json:"jti"`
	HTM   string `json:"htm"`
	HTU   string `json:"htu"`
	IAT   int64  `json:"iat"`
	Nonce string `json:"nonce"`
	ATH   string `json:"ath,omitempty"`
}

func (s *HTTPService) authenticateGuestLocked(r *http.Request, token string) (PublicKey, error) {
	values := r.Header.Values("DPoP")
	if len(values) != 1 || len(values[0]) > 4096 {
		return PublicKey{}, ErrNotAuthorized
	}
	parts := strings.Split(values[0], ".")
	if len(parts) != 3 {
		return PublicKey{}, ErrNotAuthorized
	}
	decoded := make([][]byte, 3)
	for i, p := range parts {
		b, e := rawURL.DecodeString(p)
		if e != nil || rawURL.EncodeToString(b) != p {
			return PublicKey{}, ErrNotAuthorized
		}
		decoded[i] = b
	}
	var h proofHeader
	var c proofClaims
	if strictJSON(decoded[0], &h) != nil || strictJSON(decoded[1], &c) != nil || h.Type != "dpop+jwt" || h.Algorithm != "ES256" || len(decoded[2]) != 64 {
		return PublicKey{}, ErrNotAuthorized
	}
	key, err := publicKey(h.JWK)
	if err != nil {
		return PublicKey{}, ErrNotAuthorized
	}
	now := s.now()
	if !canonicalID(c.JTI) || c.HTM != r.Method || c.HTU != s.origin+r.URL.Path || c.IAT < now/1000-60 || c.IAT > now/1000+60 {
		return PublicKey{}, ErrNotAuthorized
	}
	if token == "" {
		if c.ATH != "" || r.Header.Get("Authorization") != "" {
			return PublicKey{}, ErrNotAuthorized
		}
	} else if !validSecret(token) || !sameHash(c.ATH, hashSecret(token)) {
		return PublicKey{}, ErrNotAuthorized
	}
	expiry, ok := s.nonces[c.Nonce]
	if !ok || expiry <= now || s.proofs[c.JTI] > now {
		return PublicKey{}, ErrNotAuthorized
	}
	digest := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(key, digest[:], new(big.Int).SetBytes(decoded[2][:32]), new(big.Int).SetBytes(decoded[2][32:])) {
		return PublicKey{}, ErrNotAuthorized
	}
	delete(s.nonces, c.Nonce)
	s.proofs[c.JTI] = now + 120000
	return h.JWK, nil
}
func guestToken(r *http.Request) (string, error) {
	values := r.Header.Values("Authorization")
	if len(values) != 1 || !strings.HasPrefix(values[0], "DPoP ") {
		return "", ErrNotAuthorized
	}
	token := strings.TrimPrefix(values[0], "DPoP ")
	if !validSecret(token) {
		return "", ErrNotAuthorized
	}
	return token, nil
}
func fail(w http.ResponseWriter, status int, code string) {
	writeResponse(w, status, map[string]string{"error": code})
}
func writeResponse(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func errorResponse(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ErrInactive):
		fail(w, 410, "inactive")
	case errors.Is(err, ErrCapacity):
		fail(w, 429, "capacity")
	case errors.Is(err, ErrNotAuthorized):
		fail(w, 401, "not_authorized")
	case errors.Is(err, ErrRevision):
		fail(w, 409, "revision_mismatch")
	case errors.Is(err, ErrInvalid):
		fail(w, 400, "invalid_request")
	default:
		fail(w, 503, "unavailable")
	}
}
