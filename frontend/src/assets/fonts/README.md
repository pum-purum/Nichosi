# Bundled fonts

DM Sans (Latin variable, weight 100–1000) and JetBrains Mono (Latin and Cyrillic
variable, weight 100–800) are self-hosted WOFF2 fonts under the SIL Open Font
License 1.1. DM Sans uses system fallbacks for Cyrillic interface labels.

Font files were obtained from the Fontsource CDN:
- https://cdn.jsdelivr.net/fontsource/fonts/dm-sans:vf@latest/latin-wght-normal.woff2
- https://cdn.jsdelivr.net/fontsource/fonts/jetbrains-mono:vf@latest/latin-wght-normal.woff2
- https://cdn.jsdelivr.net/fontsource/fonts/jetbrains-mono:vf@latest/cyrillic-wght-normal.woff2

Upstream projects and license sources:
- https://github.com/google/fonts/tree/main/ofl/dmsans
- https://github.com/google/fonts/tree/main/ofl/jetbrainsmono

License notices are included in `frontend/public/fonts/` and copied into the
production build. Fonts are bundled by Vite; no runtime CDN requests are made.
