---
paths:
  - server/config/**/*.go
---

## Config Loading

- Viper-based config loading: `defaults.yaml` (embedded) is loaded first,
  then the first `ssoosshd.yaml` found is merged over it, searched in this
  order: `./`, `$HOME/.config/`, `$HOME/.config/ssoossh/`, `/etc/`,
  `/etc/ssoossh/` (`server/config/config.go`). `--config`/`-c` bypasses
  the search.
