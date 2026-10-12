package main

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
	"text/template"
	"time"
)

func TestInstallerMQTTNamespaceResolutionAndPreservation(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	start := strings.Index(script, "resolve_mqtt_namespace() {")
	if start < 0 {
		t.Fatal("missing namespace resolver")
	}
	end := strings.Index(script[start:], "\nmqtt_namespace=")
	if end < 0 {
		t.Fatal("missing namespace invocation")
	}
	resolver := script[start : start+end]
	for _, tc := range []struct{ name, setup, want string }{
		{"explicit", `MQTT_NAMESPACE=operator; TESLAMATE_CONTAINER=running`, "operator"},
		{"explicit-empty", `MQTT_NAMESPACE=''; TESLAMATE_CONTAINER=running`, ""},
		{"live", `unset MQTT_NAMESPACE; TESLAMATE_CONTAINER=running`, "live-ns"},
		{"live-default-over-stale", `unset MQTT_NAMESPACE; TESLAMATE_CONTAINER=default`, ""},
		{"prior-offline", `unset MQTT_NAMESPACE; TESLAMATE_CONTAINER=''`, "prior-ns"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// Execute the exact resolver function, never the installer itself.
			stub := `set -euo pipefail
TESLAMATE_DIR=/nonexistent-myt-namespace-test
ENV_FILE=/nonexistent-myt-prior-test
read_live_mqtt_namespace() { if [[ "$1" == running ]]; then printf live-ns; else return 0; fi; }
read_env_value() { printf prior-ns; }
`
			out, err := exec.Command("bash", "-c", stub+resolver+"\n"+tc.setup+"\nresolve_mqtt_namespace").CombinedOutput()
			if err != nil || string(out) != tc.want {
				t.Fatalf("namespace=%q want %q err=%v", out, tc.want, err)
			}
		})
	}
	for _, key := range []string{"MQTT_NAMESPACE", "DATABASE_MAX_OPEN_CONNS", "DATABASE_MAX_IDLE_CONNS", "DATABASE_CONN_MAX_IDLE_TIME", "DATABASE_CONN_MAX_LIFETIME"} {
		if !strings.Contains(script, "printf '"+key+"=%s\\n'") || !strings.Contains(script, key+": \\${") {
			t.Fatalf("installer drops config %s", key)
		}
	}
}

func TestInstallerNamespaceValidationMatchesRuntime(t *testing.T) {
	for _, value := range []string{"", "home-1", "garage_2", "site.3", "a/b", "+", "#", " home ", "with space", "a\nb", "$garage", "'quoted'", "back\\slash", "車", strings.Repeat("n", 129)} {
		cmd := exec.Command("bash", "-c", `mqtt_namespace="$1"; [[ ${#mqtt_namespace} -le 128 && "$mqtt_namespace" != *[!a-zA-Z0-9_.-]* ]]`, "namespace-check", value)
		shellValid := cmd.Run() == nil
		t.Setenv("MQTT_NAMESPACE", value)
		_, err := mqttNamespace()
		if shellValid != (err == nil) {
			t.Fatalf("namespace validator mismatch for %q", value)
		}
	}
}

func TestInstallerNamespaceCaptureRejectsTrailingNewline(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	start := strings.Index(script, "resolve_mqtt_namespace() {")
	end := strings.Index(script, "\n# Accept a deliberately canonical")
	if start < 0 || end <= start {
		t.Fatal("missing namespace capture/validation block")
	}
	block := script[start:end]
	for _, value := range []string{"home", "", "home\n", "home\n\n", "\n", "home\r", "home\r\n", "home$bad", "'home'"} {
		t.Run(strings.ReplaceAll(value, "\n", "LF"), func(t *testing.T) {
			stub := `set -euo pipefail
fail() { exit 42; }
MQTT_NAMESPACE="$1"
`
			out, err := exec.Command("bash", "-c", stub+block+`printf '%s' "$mqtt_namespace"`, "namespace-capture", value).CombinedOutput()
			t.Setenv("MQTT_NAMESPACE", value)
			_, validationErr := mqttNamespace()
			if (err == nil) != (validationErr == nil) {
				t.Fatalf("capture/validation mismatch for %q: %q err=%v", value, out, err)
			}
			if err == nil && string(out) != value {
				t.Fatalf("capture normalized %q to %q", value, out)
			}
		})
	}
	// The sentinel must not turn a failed resolver into a successful assignment.
	failedResolver := strings.Replace(block, "printf '%s' \"$MQTT_NAMESPACE\"", "return 7", 1)
	if err := exec.Command("bash", "-c", "set -euo pipefail; fail() { exit 42; }; MQTT_NAMESPACE=home;\n"+failedResolver).Run(); err == nil {
		t.Fatal("sentinel masked the resolver failure")
	}
}

func TestInstallerLiveNamespaceDecoderDoesNotNormalize(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	start := strings.Index(script, "read_live_mqtt_namespace() {")
	end := strings.Index(script, "\n# Accept a deliberately canonical")
	if start < 0 || end <= start {
		t.Fatal("missing live namespace decoder/capture block")
	}
	block := script[start:end]
	formatStart := strings.Index(block, "--format '")
	if formatStart < 0 {
		t.Fatal("missing Docker inspect template")
	}
	format := block[formatStart+len("--format '"):]
	formatEnd := strings.Index(format, "'")
	if formatEnd < 0 {
		t.Fatal("unterminated Docker inspect template")
	}
	inspectTemplate, err := template.New("docker-env").Parse(format[:formatEnd])
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name    string
		entries []string
		status  string
		want    string
		valid   bool
	}{
		{"live", []string{"OTHER=value", "MQTT_NAMESPACE=home-1"}, "0", "home-1", true},
		{"empty", []string{"MQTT_NAMESPACE="}, "0", "", true},
		{"missing", []string{"OTHER=value"}, "0", "", true},
		{"last-entry", []string{"MQTT_NAMESPACE=old", "MQTT_NAMESPACE=current"}, "0", "current", true},
		{"escaped-lf", []string{"MQTT_NAMESPACE=home\n"}, "0", "", false},
		{"embedded-lf", []string{"MQTT_NAMESPACE=home\nMQTT_NAMESPACE=other"}, "0", "", false},
		{"escaped-cr", []string{"MQTT_NAMESPACE=home\r"}, "0", "", false},
		{"literal-double-quotes", []string{"MQTT_NAMESPACE=\"home\""}, "0", "", false},
		{"literal-single-quotes", []string{"MQTT_NAMESPACE='home'"}, "0", "", false},
		{"literal-backslash", []string{"MQTT_NAMESPACE=home\\other"}, "0", "", false},
		{"inspect-failure", nil, "1", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var quoted bytes.Buffer
			if err := inspectTemplate.Execute(&quoted, map[string]any{"Config": map[string]any{"Env": tc.entries}}); err != nil {
				t.Fatal(err)
			}
			stub := `set -euo pipefail
fail() { exit 42; }
unset MQTT_NAMESPACE
TESLAMATE_CONTAINER=running
docker_output="$1"
docker_status="$2"
docker() { printf '%s' "$docker_output"; return "$docker_status"; }
`
			out, err := exec.Command("bash", "-c", stub+block+`printf '%s' "$mqtt_namespace"`, "live-namespace", quoted.String(), tc.status).CombinedOutput()
			if (err == nil) != tc.valid || tc.valid && string(out) != tc.want {
				t.Fatalf("live namespace=%q valid=%t want=%q valid=%t err=%v", out, err == nil, tc.want, tc.valid, err)
			}
		})
	}
}

func TestInstallerPoolConfigurationValidation(t *testing.T) {
	contents, err := os.ReadFile("install.sh")
	if err != nil {
		t.Fatal(err)
	}
	script := string(contents)
	start := strings.Index(script, "valid_database_pool_duration() {")
	if start < 0 {
		t.Fatal("missing pool validation helpers")
	}
	end := strings.Index(script[start:], "\ndatabase_max_open_conns=")
	if end < 0 {
		t.Fatal("missing pool resolution block")
	}
	helpers := script[start : start+end]
	check := `
database_max_open_conns="$1"
database_max_idle_conns="$2"
database_conn_max_idle_time="$3"
database_conn_max_lifetime="$4"
validate_database_pool_config
`
	for _, tc := range []struct {
		name, open, idle, idleTime, lifetime string
		valid                                bool
	}{
		{"defaults", "8", "2", "5m", "30m", true},
		{"minimum", "1", "0", "1s", "1s", true},
		{"maximum", "64", "64", "24h", "24h", true},
		{"composite", "8", "2", "1h30m", "23h59m59.999999999s", true},
		{"fractions", "8", "2", "1.5s", "0.5h", true},
		{"nanoseconds", "8", "2", "1000000000ns", "1000000us", true},
		{"no-open", "0", "0", "5m", "30m", false},
		{"too-many-open", "65", "2", "5m", "30m", false},
		{"idle-exceeds-open", "1", "2", "5m", "30m", false},
		{"huge-open", strings.Repeat("9", 1000), "2", "5m", "30m", false},
		{"blank", "", "2", "5m", "30m", false},
		{"leading-zero", "08", "2", "5m", "30m", false},
		{"too-short", "8", "2", "999999999ns", "30m", false},
		{"too-long", "8", "2", "5m", "24h1ns", false},
		{"huge-duration", "8", "2", strings.Repeat("9", 1000) + "s", "30m", false},
		{"unknown-unit", "8", "2", "1d", "30m", false},
		{"fraction-below-nanosecond", "8", "2", "1.0000000001s", "30m", false},
		{"sign", "8", "2", "+5m", "30m", false},
		{"leading-zero-duration", "8", "2", "05m", "30m", false},
		{"namespace-injection", "8", "2", "5m\nMQTT_NAMESPACE=other", "30m", false},
		{"open-injection", "8\nMQTT_NAMESPACE=other", "2", "5m", "30m", false},
		{"trailing-lf", "8", "2", "5m\n", "30m", false},
		{"cr", "8", "2", "5m\r", "30m", false},
		{"dollar", "8", "2", "${OTHER}", "30m", false},
		{"quoted", "8", "2", "'5m'", "30m", false},
		{"backslash", "8", "2", "5m\\", "30m", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, err := exec.Command("bash", "-c", "set -euo pipefail;\n"+helpers+check, "pool-check", tc.open, tc.idle, tc.idleTime, tc.lifetime).CombinedOutput()
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t want %t: %q err=%v", err == nil, tc.valid, out, err)
			}
			if tc.valid {
				for _, value := range []string{tc.idleTime, tc.lifetime} {
					duration, err := time.ParseDuration(value)
					if err != nil || duration < time.Second || duration > 24*time.Hour {
						t.Fatalf("installer accepted invalid Go duration %q", value)
					}
				}
			}
		})
	}
	resolutionEnd := strings.Index(script[start:], "\nmqtt_needs_host_gateway=false")
	if resolutionEnd < 0 {
		t.Fatal("missing pool validation invocation")
	}
	resolution := script[start : start+resolutionEnd]
	for _, value := range []string{"5m\nMQTT_NAMESPACE=other", "5m\n", "${OTHER}", "'5m'", ""} {
		stub := `set -euo pipefail
fail() { exit 42; }
read_env_value() { return 1; }
ENV_FILE=/nonexistent-myt-pool-test
DATABASE_MAX_OPEN_CONNS=8
DATABASE_MAX_IDLE_CONNS=2
DATABASE_CONN_MAX_IDLE_TIME="$1"
DATABASE_CONN_MAX_LIFETIME=30m
`
		out, err := exec.Command("bash", "-c", stub+resolution+"\nprintf serialization-reached", "pool-resolution", value).CombinedOutput()
		if err == nil || string(out) != "" {
			t.Fatalf("pool resolution accepted/serialized %q: %q err=%v", value, out, err)
		}
	}
}
