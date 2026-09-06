<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { getAdminUser, disableUser, enableUser, getUserAudit } from '$lib/api/endpoints';
	import Button from '$lib/components/Button.svelte';
	import AuditTimeline from '$lib/components/AuditTimeline.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import PageShell from '$lib/components/PageShell.svelte';
	import type {
		AdminUserDetail,
		AdminUserOverride,
		AuditEvent,
		DisableUserConsequences
	} from '$lib/api/types';

	let user: AdminUserDetail | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);
	let actionBusy = $state(false);
	let showDisableConfirm = $state(false);
	let disableConsequences: DisableUserConsequences | null = $state(null);
	// Both reasons are required by the server, so the buttons stay disabled
	// until one is typed rather than letting the request fail.
	let disableReason = $state('');
	let showEnableConfirm = $state(false);
	let enableReason = $state('');
	let auditEvents: AuditEvent[] = $state([]);
	let auditError: string | null = $state(null);

	// $derived, not a plain const: on a client-side navigation the component
	// initialises before the router has populated params, so capturing the
	// value once yields an empty id. The approval page at
	// routes/approve/[id] reads it the same way for the same reason.
	const userId = $derived(page.params.id ?? '');

	/** disabledSourceLabel names what disabled an account in the terms the
	 * re-enable decision is made in. ldap_sync is the one that matters: the
	 * sync clears only its own disables, so a human disable is never undone
	 * automatically. */
	function disabledSourceLabel(source: string): string {
		switch (source) {
			case 'ldap_sync':
				return 'the directory sync (cleared automatically if the entry reappears)';
			case 'soc':
				return 'a SOC operator';
			case 'admin':
				return 'an admin';
			default:
				return source;
		}
	}

	/** reservedFields are the override destinations that have their own
	 * block above the extra fields, so the extra-fields block can annotate
	 * the rest without them leaking in. */
	const reservedFields = ['other_accounts', 'service_accounts', 'name'];

	/** overrides indexes the directory overrides by field name, so each
	 * block in the OIDC record can ask "is this one of mine" without
	 * scanning the list. A record rather than a Map because the template
	 * reads it directly. */
	const overrides: Record<string, AdminUserOverride> = $derived.by(() => {
		const list: AdminUserOverride[] = user?.directory_overrides ?? [];
		return Object.fromEntries(list.map((o) => [o.field, o]));
	});

	/** ldapOnlyExtras are fields the directory supplies that the ID token
	 * never carried. Still an override in the sense that matters: the
	 * server acts on a value with no OIDC side at all.
	 *
	 * Derived here rather than filtered in the template because svelte-check
	 * cannot narrow `user` inside a callback. */
	const ldapOnlyExtras: AdminUserOverride[] = $derived.by(() => {
		if (!user) return [];
		const extras = user.extra_fields;
		return (user.directory_overrides ?? []).filter(
			(o) => !reservedFields.includes(o.field) && !(o.field in extras)
		);
	});

	/** oidcConfigured reports whether authentication.fields names a claim
	 * that populates one of these destinations.
	 *
	 * It is what separates "the ID token carried nothing for this field"
	 * from "nothing was ever asked of the ID token for this field", and it
	 * decides how a directory value is described: a field with no
	 * configured claim has no OIDC side to override, so calling the
	 * directory value an override there names a conflict that does not
	 * exist. Unknown fields read as configured, which is the conservative
	 * answer — it keeps the fuller explanation rather than silently
	 * dropping one. */
	function oidcConfigured(field: string): boolean {
		return user?.oidc_fields?.[field] !== false;
	}

	/** accountBlocks is the other_accounts and service_accounts rows,
	 * resolved to what the page has to state: the values the server acts
	 * on, where they came from, and the losing side where there is one.
	 *
	 * Resolved here rather than in the template because the four states
	 * (directory wins, OIDC wins, claim configured and empty, no claim
	 * configured) do not nest cleanly as markup — spelling them out inline
	 * is what made this section six lines of prose per field.
	 *
	 * `values` is the effective set, not the OIDC capture. The old block
	 * led with the OIDC values struck through and buried what the server
	 * acts on in a callout underneath, which is backwards: the effective
	 * list is the answer, and the OIDC side is the footnote that makes a
	 * wrong claim mapping visible. */
	const accountBlocks = $derived.by(() => {
		const fields = [
			{ field: 'other_accounts', label: 'Other accounts', values: user?.other_accounts ?? [] },
			{ field: 'service_accounts', label: 'Service accounts', values: user?.service_accounts ?? [] }
		];

		return fields.map((block) => {
			const override = overrides[block.field];
			if (override) {
				return {
					...block,
					values: override.effective,
					source: 'ldap',
					// Only worth showing where the ID token actually carried
					// something: with no claim configured there is no losing
					// side, and the badge has already said where this came
					// from.
					replaced: override.oidc,
					claimEmptied: oidcConfigured(block.field) && override.oidc.length === 0,
					unconfigured: false
				};
			}
			const configured = oidcConfigured(block.field);
			return {
				...block,
				source: configured ? 'oidc' : '',
				replaced: [] as string[],
				claimEmptied: false,
				unconfigured: !configured
			};
		});
	});

	/** valueList normalizes a stored extra field, which keeps the shape its
	 * claim arrived in, to the list the override rows are rendered as. */
	function valueList(value: string | string[] | undefined): string[] {
		if (value === undefined || value === null) return [];
		return Array.isArray(value) ? value : [value];
	}

	async function loadUser() {
		busy = true;
		error = null;
		if (!userId) {
			error = 'No user ID provided';
			busy = false;
			return;
		}
		try {
			user = await getAdminUser(userId);
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load user';
		} finally {
			busy = false;
		}
	}

	async function openDisableConfirm() {
		if (!user) return;

		// What disabling does is now bounded: it revokes this person's
		// access. The enrollment count is here to say what it leaves alone.
		disableConsequences = { service_enrollment_count: user.service_enrollment_count };
		showDisableConfirm = true;
	}

	async function handleDisable() {
		actionBusy = true;
		try {
			await disableUser(userId, { reason: disableReason });
			await loadUser();
			await loadAudit();
			showDisableConfirm = false;
			disableConsequences = null;
			disableReason = '';
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to disable user';
		} finally {
			actionBusy = false;
		}
	}

	async function handleEnable() {
		actionBusy = true;
		try {
			await enableUser(userId, { reason: enableReason });
			await loadUser();
			await loadAudit();
			showEnableConfirm = false;
			enableReason = '';
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to enable user';
		} finally {
			actionBusy = false;
		}
	}

	// The timeline is a separate, auditor-scoped read, so its failure is
	// reported beside it rather than replacing the whole page with an error.
	async function loadAudit() {
		try {
			const page = await getUserAudit(userId, { limit: 25 });
			auditEvents = page.events;
			auditError = null;
		} catch (cause) {
			auditError = cause instanceof Error ? cause.message : 'Failed to load the audit timeline';
		}
	}

	onMount(() => {
		loadUser();
		loadAudit();
	});
</script>

<PageShell width="full">
	{#if busy}
		<div class="text-center text-ink-muted">Loading...</div>
	{:else if error}
		<div class="rounded-lg border border-danger-surface bg-danger-surface p-4 text-sm text-danger">
			{error}
		</div>
	{:else if user}
		<!-- A snippet is its own closure, so the `user` narrowed by the
		     branch above does not reach inside one. Bind it once here and
		     the heading's two snippets can read it without each re-testing
		     a value the branch has already settled. -->
		{@const account = user}
		<PageHeading eyebrow="Admin" title={account.name || account.username}>
			{#snippet sub()}
				{#if account.name}<span class="font-mono" data-testid="user-username"
						>{account.username}</span
					> ·
				{/if}{account.email || 'No email'}
			{/snippet}
			{#snippet action()}
				<div class="flex gap-2">
					{#if account.disabled_at}
						<Button
							variant="primary"
							testid="enable-user"
							disabled={actionBusy}
							onclick={() => (showEnableConfirm = true)}
						>
							Re-enable
						</Button>
					{:else}
						<Button
							variant="danger"
							testid="disable-user"
							disabled={actionBusy}
							onclick={openDisableConfirm}
						>
							{actionBusy ? 'Disabling...' : 'Disable'}
						</Button>
					{/if}
				</div>
			{/snippet}
		</PageHeading>

		<!-- The OIDC record: exactly what the ID token carried at the last
		     login, as stored on the users row. Deliberately not the merged
		     view — where the directory overrides one of these, both sides
		     are shown and the override is named, because a claim mapping
		     that is quietly wrong is invisible otherwise. -->
		<div
			class="rounded-lg border border-border-subtle bg-surface-muted p-4"
			data-testid="user-oidc-record"
		>
			<h2 class="mb-1 font-semibold text-ink">OIDC record</h2>
			<p class="mb-4 text-[13px] text-ink-muted">
				What the identity provider sent at this user's last login. The account lists below are
				badged with the source the server actually acts on, since a configured
				<code>ldap.fields</code> entry replaces its OIDC counterpart outright.
			</p>
			<div class="grid gap-4 sm:grid-cols-2">
				<div>
					<p class="text-xs font-semibold text-ink-muted">Account identifier</p>
					<p class="font-mono text-sm break-all">{user.subject}</p>
					<p class="mt-0.5 text-xs text-ink-muted">
						The claim named by <code>authentication.fields.subject</code>. The only field here that
						is stable across logins.
					</p>
				</div>
				<div>
					<p class="text-xs font-semibold text-ink-muted">Username</p>
					<p class="font-mono text-sm">{user.username}</p>
				</div>
				<div data-testid="user-name">
					<p class="text-xs font-semibold text-ink-muted">Name</p>
					<p>{user.name || 'Not captured'}</p>
					{#if overrides.name}
						<p class="mt-0.5 text-xs text-accent" data-testid="user-name-overridden">
							{oidcConfigured('name') ? 'Overridden by LDAP' : 'Supplied by LDAP'}: {overrides.name.effective.join(
								', '
							) || 'none'}
						</p>
					{/if}
				</div>
				<div>
					<p class="text-xs font-semibold text-ink-muted">Created</p>
					<p>{new Date(user.created_at).toLocaleString()}</p>
				</div>
				<div>
					<p class="text-xs font-semibold text-ink-muted">Last Updated</p>
					<p>{new Date(user.updated_at).toLocaleString()}</p>
				</div>
				{#if user.disabled_at}
					<div data-testid="user-disabled-badge">
						<p class="text-xs font-semibold text-danger">Disabled At</p>
						<p>{new Date(user.disabled_at).toLocaleString()}</p>
					</div>
					<div data-testid="user-disabled-reason">
						<p class="text-xs font-semibold text-danger">Disable Reason</p>
						<p>{user.disabled_reason || 'No reason recorded'}</p>
					</div>
					{#if user.disabled_source}
						<div data-testid="user-disabled-source">
							<p class="text-xs font-semibold text-danger">Disabled By</p>
							<!-- The source, not the person: it is what decides whether the
							     directory sync may clear this disable automatically. -->
							<p>{disabledSourceLabel(user.disabled_source)}</p>
						</div>
					{/if}
				{/if}
			</div>

			<!-- Shown even when empty and even when overridden. An empty
			     other_accounts under an LDAP override is the exact state
			     someone is trying to diagnose when they ask why a principal
			     is missing, and hiding the block answers the question with
			     silence.

			     One row per field: the values the server acts on, badged
			     with where they came from, and a single muted line for the
			     losing side where there is one. The badge is the same
			     uppercase source chip the group table below uses, so
			     "where did this come from" is answered the same way twice
			     on one page. This used to be six lines of prose per field
			     saying what the badge says. -->
			{#each accountBlocks as block (block.field)}
				<div class="mt-4" data-testid="user-oidc-{block.field}">
					<div class="flex items-baseline gap-2">
						<p class="text-xs font-semibold text-ink-muted">{block.label}</p>
						{#if block.source}
							<span
								class="rounded bg-surface px-2 py-0.5 text-[10px] text-ink-muted uppercase"
								data-testid="user-account-source-{block.field}">{block.source}</span
							>
						{/if}
					</div>

					{#if block.values.length > 0}
						<div class="mt-1 flex flex-wrap gap-2">
							{#each block.values as acct (acct)}
								<span class="rounded bg-surface px-2 py-1 text-sm">{acct}</span>
							{/each}
						</div>
					{:else if block.unconfigured}
						<!-- Both account fields default to empty in
						     authentication.fields, so the common deployment
						     populates neither from OIDC. "None" alone reads
						     as a claim that arrived empty, which sends
						     someone to check a mapping that was never
						     written. -->
						<p class="text-sm text-ink-muted" data-testid="user-oidc-unmapped-{block.field}">
							Not configured: <code>authentication.fields.{block.field}</code>
						</p>
					{:else}
						<p class="text-sm text-ink-muted">None.</p>
					{/if}

					<!-- The losing side, only when there is one. A claim
					     mapping that is quietly wrong is invisible otherwise,
					     and the two ways it goes wrong -- replaced by the
					     directory, or configured and arriving empty -- read
					     differently. -->
					{#if block.replaced.length > 0}
						<p class="mt-1 text-xs text-ink-muted" data-testid="user-override-{block.field}">
							LDAP replaced <span class="line-through">{block.replaced.join(', ')}</span> from OIDC
						</p>
					{:else if block.claimEmptied}
						<p class="mt-1 text-xs text-ink-muted" data-testid="user-override-{block.field}">
							The OIDC claim supplied nothing.
						</p>
					{/if}
				</div>
			{/each}

			{#if Object.keys(user.extra_fields).length > 0 || ldapOnlyExtras.length > 0}
				<div class="mt-4">
					<p class="text-xs font-semibold text-ink-muted">Extra fields</p>
					<div class="space-y-2">
						{#each Object.entries(user.extra_fields) as [key, value] (key)}
							<div class="flex items-start gap-2">
								<span
									class="flex-shrink-0 rounded bg-surface px-2 py-1 font-mono text-sm text-ink-muted"
									>{key}</span
								>
								<span
									class="flex-grow rounded bg-surface px-2 py-1 text-sm"
									class:line-through={overrides[key]}
									class:text-ink-muted={overrides[key]}
								>
									{valueList(value).join(', ') || '—'}
								</span>
							</div>
							{#if overrides[key]}
								<p class="pl-2 text-xs text-accent" data-testid="user-override-{key}">
									{oidcConfigured(key) ? 'Overridden by LDAP' : 'Supplied by LDAP'}: {overrides[
										key
									].effective.join(', ') || 'none'}
								</p>
							{/if}
						{/each}
						<!-- A field the directory supplies that the ID token never
						     carried. It is still an override in the sense that
						     matters: the server acts on a value with no OIDC side. -->
						{#each ldapOnlyExtras as override (override.field)}
							<div class="flex items-start gap-2" data-testid="user-override-{override.field}">
								<span
									class="flex-shrink-0 rounded bg-surface px-2 py-1 font-mono text-sm text-ink-muted"
									>{override.field}</span
								>
								<span class="flex-grow rounded bg-surface px-2 py-1 text-sm">
									{override.effective.join(', ') || '—'}
									<span class="text-xs text-accent">— from LDAP only, no OIDC claim</span>
								</span>
							</div>
						{/each}
					</div>
				</div>
			{/if}

			{#if user.disabled_by_username}
				<div class="mt-4">
					<p class="text-xs font-semibold text-danger">Disabled By</p>
					<p>{user.disabled_by_username}</p>
				</div>
			{/if}
		</div>

		<!-- Group membership. Never an authorization input: this is what the
		     server recorded for notification fan-out and display, and it is
		     what answers "why did this reach them" or "why is the group I
		     expected missing". -->
		<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
			<h2 class="mb-1 font-semibold text-ink">Group membership</h2>
			<p class="mb-4 text-[13px] text-ink-muted">
				Captured at login (OIDC) and by the directory sync (LDAP). Only group names the
				configuration references are stored, so a group missing here may simply be unconfigured.
				Never used to authorize anything.
			</p>
			<!-- With the directory off, its rows are withheld everywhere: from
			     this table, from the directory record below, and from
			     notification fan-out. Saying so beats an operator wondering
			     where the memberships they remember went. -->
			{#if !user.directory_enabled}
				<p class="mb-4 text-[13px] text-ink-muted" data-testid="user-groups-oidc-only">
					Only OIDC memberships are listed. <code>ldap.enabled</code> is false, so any directory-sourced
					rows are frozen at whatever the last sync read and are withheld here and from notification fan-out.
					They are kept on disk and come back if the directory is switched on again.
				</p>
			{/if}
			{#if user.groups.length === 0}
				<p class="text-sm text-ink-muted" data-testid="user-groups-empty">
					No group memberships have been captured for this user.
				</p>
			{:else}
				<div class="overflow-x-auto">
					<table class="w-full text-sm" data-testid="user-groups-table">
						<thead>
							<tr class="border-b border-border-subtle text-left text-xs text-ink-muted">
								<th class="py-2 pr-4 font-semibold">Group</th>
								<th class="py-2 pr-4 font-semibold">Source</th>
								<th class="py-2 pr-4 font-semibold">First seen</th>
								<th class="py-2 font-semibold">Last seen</th>
							</tr>
						</thead>
						<tbody>
							{#each user.groups as group (group.source + '/' + group.name)}
								<tr class="border-b border-border-subtle last:border-0">
									<td class="py-2 pr-4 font-mono">{group.name}</td>
									<td class="py-2 pr-4">
										<span class="rounded bg-surface px-2 py-0.5 text-xs uppercase"
											>{group.source}</span
										>
									</td>
									<td class="py-2 pr-4 text-ink-muted"
										>{new Date(group.first_seen_at).toLocaleString()}</td
									>
									<td class="py-2 text-ink-muted"
										>{new Date(group.last_seen_at).toLocaleString()}</td
									>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			{/if}
		</div>

		<!-- Directory record. Present only while the directory is on and for a
		     user who has been enriched at least once; either absence is an
		     answer rather than an error, and directory_enabled is what tells
		     them apart. -->
		{#if user.directory_enabled && user.directory}
			<div
				class="rounded-lg border border-border-subtle bg-surface-muted p-4"
				data-testid="user-directory"
			>
				<h2 class="mb-1 font-semibold text-ink">Directory record</h2>
				<p class="mb-4 text-[13px] text-ink-muted">
					What the LDAP sync last read for this user, and whether their entry still resolves. Absent
					entirely while <code>ldap.enabled</code> is false, since nothing refreshes it and nothing acts
					on it.
				</p>

				<div class="grid gap-4 sm:grid-cols-2">
					<div class="sm:col-span-2">
						<p class="text-xs font-semibold text-ink-muted">Unique identifier</p>
						<p class="font-mono text-sm break-all" data-testid="user-directory-id">
							{user.directory.directory_id || 'not configured'}
						</p>
						{#if !user.directory.directory_id}
							<p class="mt-0.5 text-xs text-ink-muted">
								<code>ldap.id_attribute</code> is unset, so this entry is re-read by DN and then by
								<code>ldap.user_filter</code>. Both move when the person is renamed or moved between
								OUs, which reads as a deletion and counts toward the auto-disable.
							</p>
						{/if}
					</div>
					<div class="sm:col-span-2">
						<p class="text-xs font-semibold text-ink-muted">Distinguished name</p>
						<p class="font-mono text-sm break-all">{user.directory.dn || '—'}</p>
					</div>
					<div>
						<p class="text-xs font-semibold text-ink-muted">Entry last seen</p>
						<p>
							{user.directory.last_seen_at
								? new Date(user.directory.last_seen_at).toLocaleString()
								: 'never'}
						</p>
					</div>
					<div>
						<p class="text-xs font-semibold text-ink-muted">Last sync attempt</p>
						<p>
							{user.directory.last_synced_at
								? new Date(user.directory.last_synced_at).toLocaleString()
								: 'never'}
						</p>
					</div>
				</div>

				{#if user.directory.first_missing_at}
					<div
						class="mt-4 rounded border-l-2 border-danger bg-surface p-3 text-sm"
						data-testid="user-directory-missing"
					>
						<p class="font-semibold text-danger">Entry is currently missing</p>
						<p class="text-ink-muted">
							First missing {new Date(user.directory.first_missing_at).toLocaleString()}, observed
							by {user.directory.consecutive_misses} sync
							{user.directory.consecutive_misses === 1 ? 'pass' : 'passes'}. The auto-disable is
							decided on how long it has been missing, not on how many passes have seen it.
						</p>
					</div>
				{/if}

				{#if Object.keys(user.directory.attributes).length > 0}
					<div class="mt-4">
						<p class="mb-2 text-xs font-semibold text-ink-muted">Stored field values</p>
						<div class="space-y-2">
							{#each Object.entries(user.directory.attributes) as [field, values] (field)}
								<div class="flex items-start gap-2">
									<span
										class="flex-shrink-0 rounded bg-surface px-2 py-1 font-mono text-sm text-ink-muted"
										>{field}</span
									>
									<span class="flex-grow rounded bg-surface px-2 py-1 font-mono text-sm break-all">
										{values.length > 0 ? values.join(', ') : '—'}
									</span>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{/if}

		<!-- Notification choices: every kind the server can send, and
		     whether it reaches this person.

		     Listing only the stored rows meant the common case — someone who
		     has never opened the preferences page — rendered as an empty
		     section, which reads as "we send them nothing" and means the
		     opposite.

		     A table, and no descriptions. The registry's sentence explaining
		     when a kind fires belongs on the preferences page, where someone
		     is deciding; here it was seven paragraphs between an admin and
		     the two facts they came for, which are what this person receives
		     and what they chose. The title carries the rest. -->
		{#if user.notification_preferences.length > 0}
			<div
				class="rounded-lg border border-border-subtle bg-surface-muted p-4"
				data-testid="user-notification-preferences"
			>
				<h2 class="mb-1 font-semibold text-ink">Notification choices</h2>
				<p class="mb-4 text-[13px] text-ink-muted">
					Every notification this server can send. Rows reading <em>default</em> are ones this person
					has never changed.
				</p>
				<div class="overflow-x-auto">
					<table class="w-full text-sm" data-testid="user-notification-table">
						<thead>
							<tr class="border-b border-border-subtle text-left text-xs text-ink-muted">
								<th class="py-2 pr-4 font-semibold">Notification</th>
								<th class="py-2 pr-4 font-semibold">Kind</th>
								<th class="py-2 pr-4 font-semibold">Sends</th>
								<th class="py-2 font-semibold">Changed</th>
							</tr>
						</thead>
						<tbody>
							{#each user.notification_preferences as pref (pref.kind)}
								<tr
									class="border-b border-border-subtle last:border-0"
									data-testid="user-notification-{pref.kind}"
								>
									<td class="py-2 pr-4">
										{pref.title || pref.kind}
										{#if !pref.registered}
											<!-- A stored row for a kind this build no
											     longer has. Kept rather than dropped:
											     the choice is real, still on disk, and
											     comes back if the kind does. One word,
											     because that is the whole fact. -->
											<span
												class="ml-1 text-[11px] text-ink-muted"
												data-testid="user-notification-retired">(retired)</span
											>
										{/if}
									</td>
									<td class="py-2 pr-4 font-mono text-[11px] text-ink-muted">{pref.kind}</td>
									<td
										class="py-2 pr-4 font-semibold"
										class:text-danger={!pref.enabled}
										class:text-granted={pref.enabled}
									>
										{pref.enabled ? 'on' : 'off'}
									</td>
									<!-- The date alone, not the time: a preference
									     change is not an incident timestamp, and the
									     column has to stay narrow enough to sit beside
									     three others. -->
									<td class="py-2 text-ink-muted">
										{pref.explicit && pref.updated_at
											? new Date(pref.updated_at).toLocaleDateString()
											: 'default'}
									</td>
								</tr>
							{/each}
						</tbody>
					</table>
				</div>
			</div>
		{/if}

		<!-- Activity section -->
		<div class="grid gap-4 sm:grid-cols-2">
			<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
				<p class="text-xs font-semibold text-ink-muted">Certificates</p>
				<p class="text-2xl font-bold text-accent">{user.certificate_count}</p>
			</div>
			<div class="rounded-lg border border-border-subtle bg-surface-muted p-4">
				<p class="text-xs font-semibold text-ink-muted">Active Service Enrollments</p>
				<p class="text-2xl font-bold text-accent">{user.service_enrollment_count}</p>
			</div>
		</div>

		<!-- Disable confirmation modal -->
		{#if showDisableConfirm && user && disableConsequences}
			<div class="fixed inset-0 flex items-center justify-center bg-black/50 p-4">
				<div class="w-full max-w-md rounded-lg bg-surface p-6 shadow-lg">
					<h3 class="mb-4 text-lg font-semibold text-ink">Disable User?</h3>
					<p data-testid="disable-consequences" class="mb-4 text-sm text-ink-muted">
						This will prevent <strong>{user.username}</strong> from authenticating immediately.
						{#if disableConsequences.service_enrollment_count > 0}
							The <strong>{disableConsequences.service_enrollment_count}</strong> live service enrollment(s)
							they approved keep working: those belong to their service accounts, not to this person,
							and everyone else holding the account keeps them.
						{:else}
							They have approved no live service enrollments.
						{/if}
					</p>
					<label class="mb-4 block">
						<span class="mb-1 block text-xs font-semibold text-ink-muted"> Reason (required) </span>
						<textarea
							data-testid="disable-reason"
							bind:value={disableReason}
							rows="3"
							placeholder="Why is this account being disabled? e.g. offboarded, SEC-1234"
							class="w-full rounded border border-border-subtle bg-surface-muted p-2 text-sm"
						></textarea>
						<span class="mt-1 block text-xs text-ink-muted">
							Shown to whoever decides whether to re-enable this account.
						</span>
					</label>
					<div class="flex justify-end gap-2">
						<Button
							variant="ghost"
							disabled={actionBusy}
							onclick={() => {
								showDisableConfirm = false;
								disableConsequences = null;
								disableReason = '';
							}}
						>
							Cancel
						</Button>
						<Button
							variant="danger"
							testid="confirm-disable"
							disabled={actionBusy || disableReason.trim() === ''}
							onclick={handleDisable}
						>
							{actionBusy ? 'Disabling...' : 'Disable'}
						</Button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Re-enable confirmation, which exists for its reason field: the
		     next reader of this account benefits from "cleared with security,
		     SEC-1234" as much as from why it was disabled. -->
		{#if showEnableConfirm && user}
			<div class="fixed inset-0 flex items-center justify-center bg-black/50 p-4">
				<div class="w-full max-w-md rounded-lg bg-surface p-6 shadow-lg">
					<h3 class="mb-4 text-lg font-semibold text-ink">Re-enable User?</h3>
					<p class="mb-4 text-sm text-ink-muted">
						This restores <strong>{user.username}</strong>'s ability to authenticate. Service
						enrollments that already expired are not restored.
					</p>
					{#if user.disabled_reason}
						<p class="mb-4 rounded bg-surface-muted p-2 text-sm">
							<span class="font-semibold text-ink-muted">Disabled because:</span>
							{user.disabled_reason}
						</p>
					{/if}
					<label class="mb-4 block">
						<span class="mb-1 block text-xs font-semibold text-ink-muted"> Reason (required) </span>
						<textarea
							data-testid="enable-reason"
							bind:value={enableReason}
							rows="3"
							placeholder="Why is this account being restored? e.g. cleared with security, SEC-1234"
							class="w-full rounded border border-border-subtle bg-surface-muted p-2 text-sm"
						></textarea>
					</label>
					<div class="flex justify-end gap-2">
						<Button
							variant="ghost"
							disabled={actionBusy}
							onclick={() => {
								showEnableConfirm = false;
								enableReason = '';
							}}
						>
							Cancel
						</Button>
						<Button
							variant="primary"
							testid="confirm-enable"
							disabled={actionBusy || enableReason.trim() === ''}
							onclick={handleEnable}
						>
							{actionBusy ? 'Enabling...' : 'Re-enable'}
						</Button>
					</div>
				</div>
			</div>
		{/if}

		<!-- Audit timeline: everything this account did and everything done
		     to it, from the same rows. -->
		<div class="rounded-lg border border-border-subtle bg-surface p-4">
			<h2 class="mb-4 font-semibold text-ink">Audit Timeline</h2>
			{#if auditError}
				<p class="text-sm text-danger">{auditError}</p>
			{:else}
				<AuditTimeline events={auditEvents} subjectUserId={userId} />
			{/if}
		</div>
	{/if}
</PageShell>
