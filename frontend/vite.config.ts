import { sveltekit } from '@sveltejs/kit/vite';
import tailwindcss from '@tailwindcss/vite';
import { defineConfig, loadEnv } from 'vite';

// Config is a function so it can call loadEnv. Vite loads .env files for
// application code automatically, but not for this file — vite.config.ts is
// evaluated before that happens, so process.env.PORT and friends would be
// undefined here. loadEnv does the same resolution explicitly: .env,
// .env.local, .env.[mode], .env.[mode].local, in that precedence order.
//
// The empty prefix ('') loads every variable rather than only VITE_-prefixed
// ones. That is safe because none of these reach the browser — they only
// configure the dev server. Anything the client should see has to go through
// SvelteKit's $env/static/public with a PUBLIC_ prefix.
export default defineConfig(({ mode }) => {
	const env = loadEnv(mode, process.cwd(), '');
	const backendURL = env.DEVELOPMENT_BACKEND_URL || 'http://localhost:8080';

	return {
		plugins: [sveltekit(), tailwindcss()],

		server: {
			host: env.HOST,
			allowedHosts: true,
			port: +(env.PORT || 3001),
			// Must mirror the prefixes the server owns — see
			// backendOwnedPrefixes in server/frontend/frontend_included.go.
			// ssoosshd serves /auth (login and OIDC callback); /oauth was
			// inherited from pocket-id and matches no route.
			//
			// Proxying rather than pointing the app at an absolute backend URL
			// is deliberate: it keeps the browser on one origin, so the session
			// cookie is first-party and Sec-Fetch-Site stays "same-origin" —
			// which is what server/middleware/csrf.go checks. A cross-origin
			// dev setup would fail CSRF in dev but pass in production, hiding
			// exactly the class of bug that middleware exists to catch.
			proxy: {
				'/api': { target: backendURL },
				'/auth': { target: backendURL },
				'/.well-known': { target: backendURL }
			}
		},

		test: {
			environment: 'jsdom',
			globals: true,
			setupFiles: ['./vitest-setup.ts'],
			include: ['src/**/*.{test,spec}.{js,ts}']
		},

		// Vitest resolves the "node" export condition by default, which for
		// Svelte 5 means the SSR build — components then render to a string
		// instead of mounting, and @testing-library/svelte cannot interact
		// with them. Only applied under Vitest so the real build is untouched.
		resolve: process.env.VITEST ? { conditions: ['browser'] } : undefined
	};
});
