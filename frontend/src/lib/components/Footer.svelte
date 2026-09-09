<script lang="ts">
	/* Every link here points off-site, at the project's own repository, so
	   resolve() has nothing to check them against — it validates route ids
	   against this app's route tree. The URLs come from the server's
	   /api/version payload, which the lint rule cannot see through. */
	/* eslint-disable svelte/no-navigation-without-resolve */
	import type { BrandingResponse, VersionResponse } from '$lib/api/generated/webtypes';

	// The bar closing every page: what build is running, and where the
	// project lives. Presentational on purpose — the caller supplies the
	// build identity and the branding so this stays trivially testable, and
	// so both fetches happen once in the layout rather than per render.
	interface Props {
		version: VersionResponse | null;
		branding?: BrandingResponse | null;
	}

	let { version, branding = null }: Props = $props();

	// Where a user reports a problem. A deployment that names its own
	// support address takes over this slot entirely: end users filing
	// GitHub issues about someone else's SSH infrastructure helps nobody,
	// and an operator who set a support contact has already said who
	// handles this. The project links (the GitHub mark, the release tag)
	// stay put — those are attribution, not a support funnel.
	//
	// The label falls back to a generic action rather than to the raw
	// address, unlike the login page: a bare address reads as a caption in
	// a sentence and as noise in a row of nav links.
	const supportEmail = $derived(branding?.support_email ?? '');
	const supportLabel = $derived(branding?.support_label || 'Contact support');

	/** shortCommit trims a git sha to the usual seven characters, and leaves
	 * anything that is not a sha (the unstamped "commit" default) alone. */
	function shortCommit(commit: string): string {
		return /^[0-9a-f]{7,40}$/i.test(commit) ? commit.slice(0, 7) : '';
	}

	const commit = $derived(shortCommit(version?.commit ?? ''));

	// A tagged build is named by its release. An untagged one carries the
	// word "development", which identifies nothing on its own, so the commit
	// stands in for the version there.
	const label = $derived.by(() => {
		if (!version) {
			return '';
		}
		if (version.release_url) {
			return version.version.startsWith('v') ? version.version : `v${version.version}`;
		}
		return commit ? `${version.version} (${commit})` : version.version;
	});
</script>

{#if version}
	<footer class="border-t border-border-subtle bg-surface">
		<div
			class="flex flex-col items-center justify-between gap-3 px-8 py-4 text-xs text-ink-muted sm:flex-row"
		>
			<p>
				<span>ssoossh</span>
				{#if version.release_url}
					<a
						href={version.release_url}
						target="_blank"
						rel="noopener noreferrer"
						class="hover:text-ink"
						title={commit ? `commit ${commit}` : undefined}>{label}</a
					>
				{:else}
					<span title={commit ? `commit ${commit}` : undefined}>{label}</span>
				{/if}
			</p>

			<nav aria-label="Project links" class="flex items-center gap-4">
				<a
					href={version.github_url}
					target="_blank"
					rel="noopener noreferrer"
					class="flex items-center gap-1.5 hover:text-ink"
				>
					<!-- The GitHub mark, inline. Tabler does ship brand-github and
					     brand-github-filled, but both are redrawn on its own grid;
					     this is the official mark, and a project link is the one
					     place where the real one is worth keeping. -->
					<svg
						viewBox="0 0 16 16"
						width="14"
						height="14"
						fill="currentColor"
						aria-hidden="true"
						class="flex-shrink-0"
					>
						<path
							d="M8 0C3.58 0 0 3.58 0 8c0 3.54 2.29 6.53 5.47 7.59.4.07.55-.17.55-.38 0-.19-.01-.82-.01-1.49-2.01.37-2.53-.49-2.69-.94-.09-.23-.48-.94-.82-1.13-.28-.15-.68-.52-.01-.53.63-.01 1.08.58 1.23.82.72 1.21 1.87.87 2.33.66.07-.52.28-.87.51-1.07-1.78-.2-3.64-.89-3.64-3.95 0-.87.31-1.59.82-2.15-.08-.2-.36-1.02.08-2.12 0 0 .67-.21 2.2.82.64-.18 1.32-.27 2-.27.68 0 1.36.09 2 .27 1.53-1.04 2.2-.82 2.2-.82.44 1.1.16 1.92.08 2.12.51.56.82 1.27.82 2.15 0 3.07-1.87 3.75-3.65 3.95.29.25.54.73.54 1.48 0 1.07-.01 1.93-.01 2.2 0 .21.15.46.55.38A8.012 8.012 0 0 0 16 8c0-4.42-3.58-8-8-8z"
						/>
					</svg>
					GitHub
				</a>
				{#if supportEmail}
					<a href={`mailto:${supportEmail}`} class="hover:text-ink">{supportLabel}</a>
				{:else}
					<a
						href={`${version.github_url}/issues`}
						target="_blank"
						rel="noopener noreferrer"
						class="hover:text-ink">Report an issue</a
					>
				{/if}
			</nav>
		</div>
	</footer>
{/if}
