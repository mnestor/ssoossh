---
paths:
  - frontend/src/**/*.ts
  - frontend/src/**/*.svelte
---

# Frontend Rules

- App code takes wire values from the generated constants in
  `frontend/src/lib/api/generated/` (audit actions and namespaces, enums,
  API types), which tygo builds from the Go declarations. Never re-spell
  them as string literals: a renamed or removed value then fails
  `svelte-check` instead of silently matching nothing. Tests may keep
  literal wire values on purpose, to pin what the server sends.
