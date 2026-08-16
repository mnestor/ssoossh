import adapter from '@sveltejs/adapter-static';
import { vitePreprocess } from '@sveltejs/vite-plugin-svelte';
import packageJson from './package.json' with { type: 'json' };

/** @type {import('@sveltejs/kit').Config} */
const config = {
	// Consult https://kit.svelte.dev/docs/integrations#preprocessors
	// for more information about preprocessors
	preprocess: vitePreprocess(),

	kit: {
		// adapter-auto only supports some environments, see https://kit.svelte.dev/docs/adapter-auto for a list.
		// If your environment is not supported, or you settled on a specific environment, switch out the adapter.
		// See https://kit.svelte.dev/docs/adapters for more information about adapters.
		// Output straight into the Go module that embeds it
		// (server/frontend/frontend_included.go's //go:embed all:dist/*).
		// BUILD_OUTPUT_PATH overrides it for one-off builds.
		adapter: adapter({
			fallback: 'index.html',
			pages: process.env.BUILD_OUTPUT_PATH ?? '../server/frontend/dist'
		}),
		version: {
			name: packageJson.version
		}
	}
};

export default config;
