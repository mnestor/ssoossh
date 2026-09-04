<script lang="ts">
	import { onMount } from 'svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import SearchInput from '$lib/components/SearchInput.svelte';
	import { getAdminConfig } from '$lib/api/endpoints';
	import type { ConfigSection, ConfigSetting, EffectiveConfigResponse } from '$lib/api/types';

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
	let busy = $state(false);
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
			error = cause instanceof Error ? cause.message : 'Failed to load configuration';
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
</script>

<div class="flex w-full flex-col gap-5">
	<PageHeading eyebrow="Admin" title="Server configuration" />

	<p class="-mt-2 text-[13px] text-ink-muted">
		Every key in effect on this server, read-only. Secrets are redacted; a redacted key still says
		whether a value is set.
	</p>

	{#if busy}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if error}
		<div
			data-testid="config-error"
			class="rounded-[10px] border border-danger-surface bg-danger-surface p-4 text-sm text-danger"
		>
			{error}
		</div>
	{:else if config}
		<div class="flex flex-col gap-3 sm:flex-row sm:items-center">
			<div class="flex-1">
				<SearchInput
					label="Filter configuration keys"
					placeholder="Filter by key or value"
					testid="config-search"
					onsearch={(term: string) => (query = term)}
				/>
			</div>
			<label class="flex items-center gap-2 text-[13px] whitespace-nowrap text-ink-muted">
				<input type="checkbox" bind:checked={showUnset} class="accent-accent" />
				Show unset keys
			</label>
		</div>

		<p data-testid="config-count" class="text-xs text-ink-muted">
			{setCount} of {all.length} keys set
		</p>

		{#if sections.length === 0}
			<p data-testid="config-empty" class="text-sm text-ink-muted">
				No configuration key matches this filter.
			</p>
		{:else}
			<div class="flex flex-col gap-2.5">
				{#each sections as section (section.name)}
					<section
						data-testid="config-section"
						class="rounded-[10px] border border-border-subtle bg-surface p-4"
					>
						<h2
							class="mb-2 font-mono text-[11px] font-semibold tracking-[0.06em] text-ink-muted uppercase"
						>
							{section.name}
						</h2>
						<dl class="flex flex-col">
							{#each section.settings as setting (setting.key)}
								<div
									class="flex flex-col gap-0.5 border-t border-border-subtle py-1.5 first:border-t-0 first:pt-0 sm:flex-row sm:items-baseline sm:gap-4"
								>
									<dt class="font-mono text-[12px] break-all text-ink-muted sm:w-[45%] sm:shrink-0">
										{setting.key}
									</dt>
									<dd class="flex items-baseline gap-2 font-mono text-[12px] break-all">
										{#if setting.value === ''}
											<span class="text-ink-muted italic">not set</span>
										{:else}
											<span>{setting.value}</span>
										{/if}
										{#if setting.secret}
											<span
												class="shrink-0 rounded border border-border-subtle px-1 text-[10px] tracking-[0.04em] text-ink-muted uppercase"
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
</div>
