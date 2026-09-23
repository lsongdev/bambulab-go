# Bundled printer CA

`printer.cer` is the public printer CA bundle from Bambu Studio's
`resources/cert/printer.cer` (also distributed in the macOS application at
`Contents/Resources/cert/printer.cer`). Copied from the locally installed
Bambu Studio on 2026-09-23.

SHA-256: `36f2bcee347ec7adce719b5fd350099591a4d3d0ec4e039c7019890d78e152a0`

The bundle contains public certificates, not private keys. Bambu may update
its certificate chain; CLI users can override this bundle with `--ca FILE`.
