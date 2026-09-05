// @ts-check
import { readFileSync } from 'node:fs';
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import mermaid from 'astro-mermaid';
import starlightOpenAPI, { openAPISidebarGroups } from 'starlight-openapi';

// Two sidebar fragments are generated alongside the pages they order, so
// the sidebar keeps the source's declaration order (Starlight's autogenerate
// sorts directories first, then alphabetically):
//
//   - config-sidebar.json, by `make confdocs`, follows the config structs
//   - cli-sidebar.json, by `make clidocs`, follows the cobra command trees
const configSidebar = JSON.parse(
	readFileSync(new URL('./config-sidebar.json', import.meta.url), 'utf8'),
);
const cliSidebar = JSON.parse(
	readFileSync(new URL('./cli-sidebar.json', import.meta.url), 'utf8'),
);

// Deployed to GitHub Pages, so the site lives under the repository name.
export default defineConfig({
	site: 'https://mnestor.github.io',
	base: '/ssoossh',
	integrations: [
		// Renders ```mermaid fences client-side, following the site's theme.
		// Must precede starlight so its markdown transform is registered
		// before Starlight's own.
		mermaid({ theme: 'neutral', autoTheme: true }),
		starlight({
			title: 'ssoossh',
			description: 'Short-lived SSH certificates from your identity provider.',
			// The app's design system, applied to the docs; see
			// frontend/DESIGN.md for the tokens these mirror.
			customCss: ['./src/styles/ssoossh.css'],
			components: {
				// Adds the app's accent eyebrow above the page heading.
				PageTitle: './src/components/PageTitle.astro',
			},
			social: [
				{ icon: 'github', label: 'GitHub', href: 'https://github.com/mnestor/ssoossh' },
			],
			plugins: [
				// The HTTP API reference, rendered from the spec `make openapi`
				// produces in the monorepo's docs/ directory.
				starlightOpenAPI([
					{
						base: 'reference/api',
						schema: '../docs/openapi.yaml',
						label: 'HTTP API',
						sidebar: { operations: { labels: 'summary', sort: 'document' } },
					},
				]),
			],
			sidebar: [
				{ label: 'Getting started', slug: 'getting-started' },
				{ label: 'How it works', items: [{ autogenerate: { directory: 'concepts' } }] },
				{ label: 'User guide', items: [{ autogenerate: { directory: 'guides' } }] },
				{ label: 'Host administration', items: [{ autogenerate: { directory: 'hosts' } }] },
				{ label: 'Server operations', items: [{ autogenerate: { directory: 'operations' } }] },
				{ label: 'Examples', items: [{ autogenerate: { directory: 'examples' } }] },
				{
					label: 'Reference',
					items: [
						{ label: 'Configuration (ssoosshd.yaml)', items: configSidebar },
						...cliSidebar,
						...openAPISidebarGroups,
					],
				},
				{ label: 'Internals', items: [{ autogenerate: { directory: 'internals' } }] },
				{ label: 'Project', items: [{ autogenerate: { directory: 'project' } }] },
			],
		}),
	],
});
