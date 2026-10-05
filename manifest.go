package manifest

import _ "embed"

// ManifestJSON is the embedded muxcore.json, the single source of this
// module's version (ADR-0021). Info().Version is derived from it via
// modulesdk.ManifestVersion.
//
//go:embed muxcore.json
var ManifestJSON []byte
