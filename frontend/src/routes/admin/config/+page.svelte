<script lang="ts">
	import { onMount } from 'svelte';
	import Alert from '#lib/components/Alert.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SearchInput from '#lib/components/SearchInput.svelte';
	import Button from '#lib/components/Button.svelte';
	import EmptyState from '#lib/components/EmptyState.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { configDocsURL } from '#lib/configdocs.js';
	import { getAdminConfig } from '#lib/api/endpoints.js';
	import type { ConfigSection, ConfigSetting, EffectiveConfigResponse } from '#lib/api/types.js';

	// The whole effective configuration, section by section. The server
	// reflects over its own config struct to build this, so the page renders
	// whatever it is handed rather than naming fields: a screen that lists
	// keys by hand is wrong the moment one is added, and an operator reading
	// it cannot tell an unset key from an unlisted one.

	// The type argument rather than an annotation on the `let`: the value is
	// only ever assigned from inside loadConfig, and TypeScript's flow
	// analysis does not see an assignment made in a callback. Annotating the
	// declaration alone leaves `config` narrowed to null for the $derived
	// below, which then cannot see a field on it.
	let config = $state<EffectiveConfigResponse | null>(null);
	let error: string | null = $state(null);
	let busy = $state(true);
	let query = $state('');

	// Most of a deployment's keys sit at their defaults, and a wall of them
	// buries the handful an operator actually set. They are one click away
	// rather than gone, because "what is this server's rate limit" is a
	// question about a key nobody set. A typed filter overrides this
	// entirely: asking for a key by name is asking whether it is set, and
	// answering "no match" to a key that exists would be a lie.
	let showUnset = $state(false);

	async function loadConfig() {
		busy = true;
		error = null;
		try {
			config = await getAdminConfig();
		} catch (cause) {
			// An expired session is a sign-in, not a failed load.
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			error = errorMessage(cause);
		} finally {
			busy = false;
		}
	}

	onMount(loadConfig);

	/** matches tests one setting against the filter. Keys and values both
	 * count: an operator searching "postgres" or "8443" is asking where a
	 * value came from as often as what a key holds. */
	function matches(setting: ConfigSetting, needle: string): boolean {
		if (needle === '') {
			return true;
		}
		return (
			setting.key.toLowerCase().includes(needle) || setting.value.toLowerCase().includes(needle)
		);
	}

	/** shown applies the filter and the unset toggle to every section,
	 * dropping a section entirely when nothing in it survives. */
	function shown(sections: ConfigSection[], needle: string, unset: boolean): ConfigSection[] {
		const keepUnset = unset || needle !== '';
		return sections
			.map((section) => ({
				...section,
				settings: section.settings.filter(
					(setting) => (keepUnset || setting.value !== '') && matches(setting, needle)
				)
			}))
			.filter((section) => section.settings.length > 0);
	}

	const loaded: ConfigSection[] = $derived(config === null ? [] : config.sections);
	const needle = $derived(query.toLowerCase());
	const sections = $derived(shown(loaded, needle, showUnset));

	// Counted before filtering, so the line states the size of the
	// configuration rather than the size of the current search.
	const all = $derived(loaded.flatMap((section) => section.settings));
	const setCount = $derived(all.filter((setting) => setting.value !== '').length);

	// How many keys the filter leaves on screen, announced so a screen
	// reader hears the result of typing (WCAG 4.1.3).
	const shownCount = $derived(sections.reduce((n, section) => n + section.settings.length, 0));
	const filterStatus = $derived(
		needle === ''
			? ''
			: `${shownCount} ${shownCount === 1 ? 'key matches' : 'keys match'} "${query}".`
	);

	// Clearing remounts the box: it owns what is typed in it.
	let searchKey = $state(0);
	function clearFilter() {
		query = '';
		searchKey += 1;
	}
</script>

<svelte:head><title>Server configuration · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Server configuration">
		{#snippet sub()}
			Every key in effect on this server, read-only. Secrets are redacted; a redacted key still says
			whether a value is set.
		{/snippet}
	</PageHeading>

	{#if busy}
		<LoadingBlock
			shape="lines"
			count={6}
			label="Loading the configuration…"
			testid="config-loading"
		/>
	{:else if error}
		<Alert
			variant="error"
			title="Could not load the configuration"
			onretry={loadConfig}
			testid="config-error"
		>
			{error}
		</Alert>
	{:else if config}
		<div class="flex flex-col gap-3 sm:flex-row sm:items-center">
			<div class="flex-1">
				{#key searchKey}
					<SearchInput
						label="Filter configuration keys"
						placeholder="Filter by key or value"
						testid="config-search"
						value={query}
						onsearch={(term: string) => (query = term)}
					/>
				{/key}
			</div>
			<label
				class="flex min-h-target items-center gap-2 text-dense whitespace-nowrap text-ink-muted"
			>
				<input type="checkbox" bind:checked={showUnset} disabled={needle !== ''} />
				Show unset keys
				{#if needle !== ''}
					<!-- A filter searches every key, set or not, so the box
					     cannot narrow it; saying so keeps the box and the list
					     from disagreeing. -->
					<span class="text-xs" data-testid="config-unset-note">(included while filtering)</span>
				{/if}
			</label>
		</div>

		<ListStatus message={filterStatus} />

		<p data-testid="config-count" class="text-xs text-ink-muted">
			{setCount} of {all.length} keys set
		</p>

		{#if sections.length === 0}
			<EmptyState icon="filter-off" title="Nothing matches" testid="config-empty">
				{#snippet action()}
					<Button variant="ghost" onclick={clearFilter} testid="config-clear-filter"
						>Clear filter</Button
					>
				{/snippet}
				No configuration key or value matches this filter.
			</EmptyState>
		{:else}
			<!-- One column, not a two-abreast grid of boxes. Sections run from
			     one key to over fifty, so the grid was forever putting a wall
			     of keys beside almost nothing however it was weighted — and a
			     configuration is one list read top to bottom, in the order the
			     file is written, which a masonry layout cannot preserve. The
			     key column is capped rather than a percentage: at this page's
			     full width, 45% would put a value half a screen away from the
			     key it belongs to. -->
			<div class="flex flex-col gap-8">
				{#each sections as section (section.name)}
					<section data-testid="config-section" class="rounded-lg bg-surface p-5">
						<h2 class="mb-1.5 text-meta font-semibold tracking-label text-ink-muted uppercase">
							{section.name}
						</h2>
						<dl class="flex flex-col divide-y divide-border-subtle">
							{#each section.settings as setting (setting.key)}
								<div class="flex flex-col gap-0.5 py-1.5 sm:flex-row sm:items-baseline sm:gap-4">
									<dt class="font-mono text-xs break-all text-ink-muted sm:w-88 sm:shrink-0">
										<!-- The key links to its reference entry, which says what it
										     governs. configDocsURL is an external docs-site URL, not
										     an app route, so resolve() does not apply. Block form
										     because the report lands on the href line. -->
										<!-- eslint-disable svelte/no-navigation-without-resolve -->
										<a
											href={configDocsURL(setting.key)}
											target="_blank"
											rel="noopener noreferrer"
											class="inline-flex items-center gap-0.5 hover:text-accent hover:underline"
											data-testid="config-key-link"
											>{setting.key}<Icon name="arrow-up-right" size="xs" /><span class="sr-only"
												>(reference, opens in a new tab)</span
											></a
										>
										<!-- eslint-enable svelte/no-navigation-without-resolve -->
									</dt>
									<dd class="flex items-baseline gap-2 font-mono text-xs break-all">
										{#if setting.value === ''}
											<span class="text-ink-muted italic">not set</span>
										{:else}
											<span>{setting.value}</span>
										{/if}
										{#if setting.secret}
											<span
												class="shrink-0 rounded border border-border-subtle px-1 text-micro tracking-label text-ink-muted uppercase"
											>
												secret
											</span>
										{/if}
									</dd>
								</div>
							{/each}
						</dl>
					</section>
				{/each}
			</div>
		{/if}
	{/if}
</PageShell>
