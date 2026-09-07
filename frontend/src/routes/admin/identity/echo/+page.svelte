<script lang="ts">
	import { startIdentityEcho } from '$lib/api/endpoints';
	import type { IdentityEchoPayload } from '$lib/api/types';
	import { errorMessage } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageSection from '$lib/components/PageSection.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import SectionLabel from '$lib/components/SectionLabel.svelte';

	let payload = $state<IdentityEchoPayload | null>(null);
	let readError = $state<string | null>(null);
	let starting = $state(false);
	let startError = $state<string | null>(null);

	// The result arrives in the URL fragment, which is never sent to a
	// server. Read it once, then drop it from the address bar and from
	// history, so the claims live on this page and nowhere else — not in a
	// database, not in the session, not in an access log, and not in a URL
	// somebody screenshots.
	$effect(() => {
		const fragment = window.location.hash.replace(/^#/, '');
		if (!fragment) return;

		history.replaceState(null, '', window.location.pathname);

		try {
			payload = JSON.parse(decodeBase64Url(decodeURIComponent(fragment))) as IdentityEchoPayload;
		} catch {
			readError = 'The echoed claims could not be decoded. Run the echo again.';
		}
	});

	/** decodeBase64Url decodes the base64url the callback encodes with,
	 * which has no padding and uses - and _ in place of + and /. */
	function decodeBase64Url(value: string): string {
		const padded = value.replace(/-/g, '+').replace(/_/g, '/');
		const binary = atob(padded + '='.repeat((4 - (padded.length % 4)) % 4));
		return new TextDecoder().decode(Uint8Array.from(binary, (c) => c.charCodeAt(0)));
	}

	async function handleStart() {
		starting = true;
		startError = null;
		try {
			const { authorization_url } = await startIdentityEcho();
			window.location.href = authorization_url;
		} catch (cause) {
			startError = errorMessage(cause);
			starting = false;
		}
	}

	/** consumers maps each claim name to the configured field that reads it,
	 * which is what turns a claim dump into a config-authoring tool. */
	// A plain record rather than a Map: it is rebuilt whole on every change
	// and never mutated in place, which is what the reactivity rule about
	// Map is guarding against.
	const consumers = $derived.by(() => {
		const byClaim: Record<string, string> = {};
		if (!payload) return byClaim;

		const m = payload.mapping;
		const reserved: Array<[string | undefined, string]> = [
			// Subject first: it is the claim the whole account is keyed by,
			// and the one an operator most needs to see confirmed.
			[m.subject, 'fields.subject'],
			[m.username, 'fields.username'],
			[m.name, 'fields.name'],
			[m.groups, 'fields.groups'],
			[m.other_accounts, 'fields.other_accounts'],
			[m.service_accounts, 'fields.service_accounts'],
			[m.email, 'fields.email']
		];
		for (const [claim, field] of reserved) {
			if (claim) byClaim[claim] = field;
		}
		for (const [name, claim] of Object.entries(m.extra ?? {})) {
			byClaim[claim] = `fields.extra.${name}`;
		}
		return byClaim;
	});

	/** claimRows is the token in a stable order: the claims something reads
	 * first, then the rest. The unread ones are what an operator came for,
	 * but the read ones are what confirms the config is doing anything. */
	const claimRows = $derived.by(() => {
		if (!payload) return [];
		return Object.entries(payload.claims)
			.map(([name, value]) => ({ name, value, consumer: consumers[name] }))
			.sort((a, b) => {
				if (!!a.consumer !== !!b.consumer) return a.consumer ? -1 : 1;
				return a.name.localeCompare(b.name);
			});
	});

	function renderValue(value: unknown): string {
		if (Array.isArray(value)) return value.join(', ');
		if (value === null || value === undefined) return '—';
		if (typeof value === 'object') return JSON.stringify(value);
		return String(value);
	}
</script>

<svelte:head><title>Claims echo · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading eyebrow="Admin" title="Claims echo">
		{#snippet sub()}
			What your identity provider actually sends, annotated against the configuration.
		{/snippet}
	</PageHeading>

	<PageSection
		title="How this works"
		description="The server keeps only the claims the configuration maps, so there is no stored copy to show."
		testid="echo-explainer"
	>
		<div class="flex flex-col gap-4 text-sm">
			<p class="text-ink-muted">
				Starting an echo signs you in again with <code>prompt=login</code>, and the callback renders
				the decoded token instead of establishing a session. Your current session is untouched, no
				user record is written, and no login is recorded.
			</p>
			<p class="text-ink-muted">
				The result never leaves this page. It arrives in the URL fragment, which browsers do not
				send to any server, and the address bar is cleared as soon as it has been read. Reload and
				it is gone.
			</p>
			<p class="text-ink-muted">
				It only ever shows <strong>your own</strong> claims. That answers "what fields do I get from this
				identity provider", which is the configuration question. It cannot answer "why is this other person
				missing a group" — the user detail page and the directory probe are for that.
			</p>

			<div>
				<Button testid="echo-start" busy={starting} onclick={handleStart}>
					{starting ? 'Redirecting…' : 'Show me what my IdP sends'}
				</Button>
			</div>

			{#if startError}
				<Alert variant="error" title="Could not start the echo" testid="echo-start-error">
					{startError}
				</Alert>
			{/if}
		</div>
	</PageSection>

	{#if readError}
		<Alert variant="error" title="Could not read the result" testid="echo-read-error">
			{readError}
		</Alert>
	{/if}

	{#if payload}
		<PageSection
			title="Your ID token"
			description="Every claim it carried, and which configured field consumes each one."
			testid="echo-result"
		>
			<div class="flex flex-col gap-4">
				<p class="text-[13px] text-ink-muted">
					Issued {new Date(payload.issued_at).toLocaleString()}. Nothing here was stored.
				</p>

				<div class="overflow-x-auto rounded-md border border-border-subtle">
					<table class="w-full text-[13px]">
						<thead>
							<tr
								class="border-b border-border-subtle bg-surface-muted text-left text-xs text-ink-muted"
							>
								<th class="px-3 py-2 font-semibold">Claim</th>
								<th class="px-3 py-2 font-semibold">Value</th>
								<th class="px-3 py-2 font-semibold">Status</th>
							</tr>
						</thead>
						<tbody data-testid="echo-claims">
							{#each claimRows as row (row.name)}
								<tr class="border-b border-border-subtle last:border-0">
									<td class="px-3 py-2 font-mono break-all">{row.name}</td>
									<td class="px-3 py-2 font-mono break-all">{renderValue(row.value)}</td>
									<td class="px-3 py-2">
										{#if row.consumer}
											<span
												class="rounded-full bg-granted-surface px-2 py-0.5 text-xs font-semibold text-granted"
												>{row.consumer}</span
											>
										{:else}
											<span class="text-ink-muted">unmapped</span>
										{/if}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>

				{#if payload.suggestions?.length}
					<div data-testid="echo-suggestions">
						<SectionLabel>Config that would capture what is being dropped</SectionLabel>
						<p class="mb-2 text-[13px] text-ink-muted">
							Suggestions, not decisions. Each one names the key the value would land under; review
							before committing it.
						</p>
						{#each payload.suggestions as suggestion (suggestion.claim)}
							<div class="mb-3 last:mb-0">
								<p class="flex flex-wrap items-baseline gap-2 text-[13px]">
									<MonoChip>{suggestion.claim}</MonoChip>
									<span class="text-ink-muted">{suggestion.reason}</span>
								</p>
								<pre
									class="mt-1 overflow-x-auto rounded bg-surface-muted p-3 font-mono text-[13px]">{suggestion.yaml}</pre>
							</div>
						{/each}
					</div>
				{:else}
					<p class="text-[13px] text-ink-muted" data-testid="echo-no-suggestions">
						Every claim in this token is either read by the configuration or protocol mechanics.
					</p>
				{/if}
			</div>
		</PageSection>
	{/if}
</PageShell>
