# My T Companion 1.10.64

- Add an explicit `cells_complete` field to footprint responses. It becomes true only after the coverage query has been read successfully to completion.
- Keep coverage-grid completeness separate from simplified or omitted display routes, including lightweight passport requests. New My T versions can use complete cells with verified city boundaries to calculate approximate explored city area.
- Preserve compatibility with existing clients. No TeslaMate schema change, vehicle wake-up, charging command, or notification is required.

City area still requires a supported, verified boundary in My T; missing or incomplete information remains unavailable rather than being treated as zero.
