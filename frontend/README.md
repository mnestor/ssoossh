# ssoossh frontend

Web UI for the ssoossh SSH certificate authority, built with SvelteKit 5 and Tailwind CSS.

## Design System

See [`DESIGN.md`](./DESIGN.md) for documentation on color tokens, typography, icons, component patterns, and accessibility guidelines.

## Development

Start the development server:

```sh
corepack pnpm install
corepack pnpm run dev
```

The frontend proxies API calls to the backend (default: `http://localhost:8080`). Configure this via the `DEVELOPMENT_BACKEND_URL` env var in `.env.local`.

## Building

To create a production build:

```sh
corepack pnpm run build
```

The static output is prerendered into `/server/frontend/dist`, which is embedded into the `ssoosshd` server binary via Go's `//go:embed` directive.

## Testing & Validation

```sh
# Run unit tests
corepack pnpm run test

# Type check
corepack pnpm run check

# Lint
corepack pnpm run lint
```

## Architecture Notes

- **Static prerendering**: The `@sveltejs/adapter-static` adapter precompiles all routes to static HTML at build time, with a fallback page for dynamic routes.
- **Self-hosted fonts**: Both sans and monospace typefaces are bundled locally via `@fontsource` packages; no external font requests.
- **Branding**: Org name, logo URL, and login consent notices are fetched at runtime from an unauthenticated `/api/branding` endpoint, allowing the same prerendered build to serve different deployments.
- **Version footer**: The footer's version and GitHub links come from an unauthenticated `/api/version` endpoint at runtime, for the same reason: the build stamp belongs to the Go binary serving the page, not to this bundle.
