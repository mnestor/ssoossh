<script lang="ts">
	import type { ClaimFieldResolution, IdentityEchoPayload } from '#lib/api/types.js';
	import { formatDateTime } from '#lib/format.js';
	import {
		echoDownload,
		echoYaml,
		fieldStatusWords,
		fieldTone,
		saveFile
	} from '#lib/identityEcho.js';
	import Button from './Button.svelte';
	import EchoClaimValue from './EchoClaimValue.svelte';
	import Icon from './Icon.svelte';
	import MonoChip from './MonoChip.svelte';
	import PageSection from './PageSection.svelte';
	import SectionLabel from './SectionLabel.svelte';

	// The claims echo's result, shared by the admin echo and the bootstrap
	// echo: the verdict first, then the mapping it comes from, then the
	// suggested configuration to copy (the one thing the reader came to do),
	// then the evidence: every claim, the candidates, and what nothing can
	// map. Everything is built from the payload already on the page; Copy
	// and Download make no request.
	interface Props {
		payload: IdentityEchoPayload;
	}

	let { payload }: Props = $props();

	/** consumers maps each claim name to the configured field that reads it,
	 * which is what turns a claim dump into a config-authoring tool. */
	// A plain record rather than a Map: it is rebuilt whole on every change
	// and never mutated in place, which is what the reactivity rule about
	// Map is guarding against.
	const consumers = $derived.by(() => {
		const byClaim: Record<string, string> = {};
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

	const protocol = $derived(new Set(payload.protocol_claims ?? []));

	/** claimRows is the token in a stable order: the claims something reads
	 * first, then the rest. The unread ones are what an operator came for,
	 * but the read ones are what confirms the config is doing anything.
	 * Protocol mechanics nothing reads are listed apart, after identity. */
	const allRows = $derived(
		Object.entries(payload.claims)
			.map(([name, value]) => ({
				name,
				value,
				consumer: consumers[name],
				source: payload.claim_sources?.[name]
			}))
			.sort((a, b) => {
				if (!!a.consumer !== !!b.consumer) return a.consumer ? -1 : 1;
				return a.name.localeCompare(b.name);
			})
	);
	const claimRows = $derived(allRows.filter((r) => !protocol.has(r.name)));
	const protocolRows = $derived(allRows.filter((r) => protocol.has(r.name)));
	const showSource = $derived(!!payload.claim_sources);
	const fromUserInfo = $derived(allRows.filter((r) => r.source === 'userinfo').length);

	const sourceWords: Record<string, string> = { id_token: 'ID token', userinfo: 'UserInfo' };

	/** What happened to the UserInfo call, in a sentence. */
	const userInfoSentence = $derived.by(() => {
		switch (payload.userinfo) {
			case 'used':
				return fromUserInfo === 0
					? 'UserInfo was called and added nothing the ID token lacked.'
					: `UserInfo was called and filled ${fromUserInfo} ${fromUserInfo === 1 ? 'claim' : 'claims'} the ID token lacked.`;
			case 'off':
				return 'UserInfo is off (authentication.userinfo), so only the ID token is read.';
			case 'no_endpoint':
				return 'The provider names no UserInfo endpoint, so only the ID token is read.';
			case 'paused':
				return 'UserInfo calls are paused after repeated failures, so only the ID token was read. The admin diagnostics page shows why.';
			case 'failed':
				return 'The UserInfo call failed, so only the ID token was read. The server log has the error.';
			default:
				return '';
		}
	});

	const toneClass: Record<ReturnType<typeof fieldTone>, string> = {
		granted: 'text-granted',
		danger: 'text-danger',
		trimmed: 'text-trimmed',
		muted: 'text-ink-muted'
	};

	/** The resolved value of a field row, as the claim value renderer
	 * takes it. */
	function resolvedValue(f: ClaimFieldResolution): unknown {
		return f.list ? (f.values ?? []) : (f.value ?? '');
	}

	const candidateGroups = $derived(
		(
			[
				['subject', 'Subject candidates'],
				['username', 'Username candidates'],
				['groups', 'Groups candidates']
			] as const
		)
			.map(([field, label]) => ({
				field,
				label,
				items: (payload.suggestions ?? []).filter((s) => s.field === field)
			}))
			.filter((g) => g.items.length > 0)
	);
	const extraSuggestions = $derived((payload.suggestions ?? []).filter((s) => s.field === 'extra'));

	let adminGroup = $state('');
	const yaml = $derived(echoYaml(payload, adminGroup));

	let copyState = $state<'idle' | 'copied' | 'failed'>('idle');
	async function copyYaml() {
		try {
			await navigator.clipboard.writeText(yaml);
			copyState = 'copied';
		} catch {
			copyState = 'failed';
		}
	}

	function download() {
		const { filename, blob } = echoDownload(payload, yaml);
		saveFile(filename, blob);
	}

	const sourceLabel: Record<string, string> = {
		id_token: 'ID token',
		access_token: 'Access token (unverified, not read by ssoossh)'
	};
</script>

<PageSection
	title="Would this sign in"
	description="The login's own claim mapping, run on this token. Whether the account is disabled, and what the directory adds, are decided at login and not shown here."
	testid="echo-sign-in"
>
	<div class="flex flex-col gap-2 text-sm">
		{#if payload.sign_in?.allowed}
			<p class="font-medium text-granted" data-testid="echo-sign-in-verdict">
				Yes. A login with this token gets past the claim mapping.
			</p>
		{:else}
			<p class="font-medium text-danger" data-testid="echo-sign-in-verdict">
				No. A login with this token would fail:
				<span class="font-mono wrap-anywhere">{payload.sign_in?.reason}</span>
			</p>
		{/if}
		{#if userInfoSentence}
			<p class="text-dense text-ink-muted" data-testid="echo-userinfo">{userInfoSentence}</p>
		{/if}
		<p class="text-dense text-ink-muted">
			Issued {formatDateTime(payload.issued_at)}. Nothing here was stored: reload and it is gone.
		</p>
	</div>
</PageSection>

{#if payload.fields?.length}
	<PageSection
		title="Mapping"
		description="Each authentication.fields entry, the claim it reads, and what that resolves to."
		testid="echo-mapping"
	>
		<div class="overflow-x-auto">
			<table class="data-table">
				<thead>
					<tr>
						<th scope="col">Field</th>
						<th scope="col">Claim</th>
						<th scope="col">Status</th>
						<th scope="col">Resolves to</th>
					</tr>
				</thead>
				<tbody data-testid="echo-mapping-rows">
					{#each payload.fields as f (f.field)}
						<tr>
							<td class="align-top font-mono wrap-anywhere">
								fields.{f.field}
								{#if f.required}<span class="font-sans text-ink-muted">(required)</span>{/if}
							</td>
							<td class="align-top font-mono wrap-anywhere">
								{#if f.claim}{f.claim}{:else}<span class="font-sans text-ink-muted">none</span>{/if}
							</td>
							<td class="align-top font-medium whitespace-nowrap {toneClass[fieldTone(f)]}">
								{fieldStatusWords[f.status]}
							</td>
							<td class="align-top font-mono wrap-anywhere">
								{#if f.status === 'resolved'}
									<EchoClaimValue value={resolvedValue(f)} name="field-{f.field}" />
								{/if}
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</PageSection>
{/if}

<PageSection
	title="Suggested configuration"
	description="The mapping above with each field that does not resolve replaced by the first candidate below. Built on this page; review it before it goes into the config file."
	testid="echo-yaml"
>
	<div class="flex flex-col gap-4">
		<div class="flex flex-col gap-1">
			<label for="echo-admin-group" class="text-dense font-medium">
				<code>admin.require_group</code>
			</label>
			{#if payload.admin_group_candidates?.length}
				<select
					id="echo-admin-group"
					bind:value={adminGroup}
					class="min-h-target max-w-[420px] rounded-md border border-border-control bg-surface px-3 text-base"
					data-testid="echo-admin-group"
				>
					<option value="">Not chosen yet</option>
					{#each payload.admin_group_candidates as group (group)}
						<option value={group}>{group}</option>
					{/each}
				</select>
				<p class="text-xs text-ink-muted">
					The group whose members are admins. Pick one this account is in; it is added to the YAML.
				</p>
			{:else}
				<p class="text-dense text-ink-muted" data-testid="echo-no-admin-groups">
					The groups field does not resolve in this token, so there is no group to pick. Map groups
					first; the groups candidates below are the lists this token carries.
				</p>
			{/if}
		</div>

		<pre
			class="overflow-x-auto rounded bg-surface-muted p-3 font-mono text-dense"
			data-testid="echo-yaml-text">{yaml}</pre>

		<div class="flex flex-wrap items-center gap-2">
			<Button onclick={copyYaml} testid="echo-copy-yaml">
				<Icon name={copyState === 'copied' ? 'copy-check' : 'copy'} size="sm" />
				Copy YAML
			</Button>
			<Button variant="ghost" onclick={download} testid="echo-download">
				<Icon name="download" size="sm" />
				Download result
			</Button>
			<span role="status" class="text-dense text-ink-muted" data-testid="echo-copy-status">
				{#if copyState === 'copied'}
					Copied.
				{:else if copyState === 'failed'}
					Copy failed. Select the YAML above instead.
				{/if}
			</span>
		</div>
		<p class="text-xs text-ink-muted">
			The download is this whole result and the YAML, as JSON. It is built here; the server keeps no
			copy.
		</p>
	</div>
</PageSection>

<PageSection
	title="Your ID token"
	description="{allRows.length} {allRows.length === 1
		? 'claim'
		: 'claims'}, and which configured field reads each one."
	testid="echo-result"
>
	<div class="flex flex-col gap-4">
		<!-- The shared table, sized for a token of any shape (DESIGN.md,
		     "The admin area"). Claim and value wrap only where a word
		     cannot fit; "Used by" never wraps. The scroll is a fallback
		     the wrapping should make rare, and the page itself never
		     scrolls sideways. -->
		{#snippet claimsTable(rows: typeof allRows, testid: string)}
			<div class="overflow-x-auto">
				<table class="data-table">
					<thead>
						<tr>
							<th scope="col">Claim</th>
							<th scope="col">Value</th>
							{#if showSource}<th scope="col">Source</th>{/if}
							<th scope="col">Used by</th>
						</tr>
					</thead>
					<tbody data-testid={testid}>
						{#each rows as row (row.name)}
							<tr>
								<td class="align-top font-mono wrap-anywhere">{row.name}</td>
								<td class="align-top font-mono wrap-anywhere">
									<EchoClaimValue value={row.value} name={row.name} />
								</td>
								{#if showSource}
									<td class="align-top whitespace-nowrap">
										{row.source ? (sourceWords[row.source] ?? row.source) : ''}
									</td>
								{/if}
								<td class="align-top whitespace-nowrap">
									{#if row.consumer}
										{row.consumer}
									{:else}
										<span class="text-ink-muted">unmapped</span>
									{/if}
								</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		{/snippet}

		{@render claimsTable(claimRows, 'echo-claims')}

		{#if protocolRows.length}
			<div>
				<SectionLabel>Protocol claims</SectionLabel>
				<p class="mb-2 text-dense text-ink-muted">
					JWT and OIDC mechanics nothing in the configuration reads.
				</p>
				{@render claimsTable(protocolRows, 'echo-protocol-claims')}
			</div>
		{/if}
	</div>
</PageSection>

<PageSection
	title="Candidates and suggestions"
	description="Suggestions, not decisions. Each names the key the value would land under."
	testid="echo-candidates"
>
	<div class="flex flex-col gap-6">
		{#each candidateGroups as group (group.field)}
			<div data-testid="echo-candidates-{group.field}">
				<SectionLabel>{group.label}</SectionLabel>
				{#each group.items as suggestion (suggestion.claim)}
					<div class="mb-3 last:mb-0">
						<p class="flex flex-wrap items-baseline gap-2 text-dense">
							<MonoChip>{suggestion.claim}</MonoChip>
							<span class="text-ink-muted">{suggestion.reason}</span>
						</p>
						<pre
							class="mt-1 overflow-x-auto rounded bg-surface-muted p-3 font-mono text-dense">{suggestion.yaml}</pre>
					</div>
				{/each}
			</div>
		{/each}

		{#if extraSuggestions.length}
			<div data-testid="echo-suggestions">
				<SectionLabel>Config that would capture what is being dropped</SectionLabel>
				{#each extraSuggestions as suggestion (suggestion.claim)}
					<div class="mb-3 last:mb-0">
						<p class="flex flex-wrap items-baseline gap-2 text-dense">
							<MonoChip>{suggestion.claim}</MonoChip>
							<span class="text-ink-muted">{suggestion.reason}</span>
						</p>
						<pre
							class="mt-1 overflow-x-auto rounded bg-surface-muted p-3 font-mono text-dense">{suggestion.yaml}</pre>
					</div>
				{/each}
			</div>
		{:else}
			<p class="text-dense text-ink-muted" data-testid="echo-no-suggestions">
				Every claim in this token is either read by the configuration or protocol mechanics.
			</p>
		{/if}
	</div>
</PageSection>

{#if payload.not_mappable?.length}
	<PageSection
		title="Not mappable"
		description="Claims no authentication.fields entry can read. The mapper reads the ID token's top-level claims only."
		testid="echo-not-mappable"
	>
		<div class="overflow-x-auto">
			<table class="data-table">
				<thead>
					<tr>
						<th scope="col">Claim</th>
						<th scope="col">Where</th>
						<th scope="col">Value</th>
						<th scope="col">Why</th>
					</tr>
				</thead>
				<tbody>
					{#each payload.not_mappable as u (u.source + ':' + u.claim)}
						<tr>
							<td class="align-top font-mono wrap-anywhere">{u.claim}</td>
							<td class="align-top">{sourceLabel[u.source] ?? u.source}</td>
							<td class="align-top font-mono wrap-anywhere">
								<EchoClaimValue value={u.value} name="{u.source}-{u.claim}" />
							</td>
							<td class="align-top text-ink-muted">{u.reason}</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
	</PageSection>
{/if}
