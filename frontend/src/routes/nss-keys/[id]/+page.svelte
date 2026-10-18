<script lang="ts">
	import { beforeNavigate } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import {
		clearNSSKeyPrevious,
		getNSSKey,
		getNSSKeySettings,
		getNSSKeyUsage,
		getNSSRotationReport,
		revokeNSSKey,
		rotateNSSKey,
		updateNSSKey
	} from '#lib/api/endpoints.js';
	import {
		NSSKeyPermissionAssignGroup,
		NSSKeyPermissionEdit,
		NSSKeyPermissionRevoke,
		NSSKeyPermissionRotate,
		NSSKeyStatusActive,
		NSSKeyStatusExpired,
		NSSKeyStatusRevoked
	} from '#lib/api/generated/enums.js';
	import type {
		NSSKey,
		NSSKeySecret,
		NSSKeySettings,
		NSSKeyUsageRow,
		UpdateNSSKeyBody
	} from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import ConfirmModal from '#lib/components/ConfirmModal.svelte';
	import CopyableId from '#lib/components/CopyableId.svelte';
	import DetailRow from '#lib/components/DetailRow.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import KeyStatusBadge from '#lib/components/KeyStatusBadge.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import LoadMoreFooter from '#lib/components/LoadMoreFooter.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SecretPanel from '#lib/components/SecretPanel.svelte';
	import { formatDateTime, formatDuration, remainingLabel } from '#lib/format.js';
	import { describeLoaded } from '#lib/listStatus.js';
	import {
		can,
		expiryTone,
		formatPercent,
		graceEnd,
		keyAlerts,
		keyStatus,
		missRate,
		previousLive,
		renewedExpiry
	} from '#lib/nss.js';

	// One fleet key: what it is, its secret's state, who may change what,
	// and how hosts are using it.
	//
	// The actions follow the key's `permissions` from the server, so a
	// reader is never offered a button that can only answer 403; the server
	// still decides. Rotating is the action this page is usually opened
	// for, so it is the one primary button, in the Secret section beside
	// the facts it changes. Revoking ends the key and sits in the heading,
	// behind a typed confirmation of the key id. Clearing the previous
	// secret early is the dangerous half of a rotation, so its dialog says
	// how many hosts the rotation report still sees on the old secret.

	const id = $derived(page.params.id ?? '');

	let key = $state<NSSKey | null>(null);
	let loadError = $state<string | null>(null);
	let notFound = $state(false);
	let hasLoaded = $state(false);
	let attempt = $state(0);

	let now = $state(new Date());
	$effect(() => {
		const timer = setInterval(() => (now = new Date()), 60_000);
		return () => clearInterval(timer);
	});

	// What a screen reader hears after an edit or an action lands.
	let liveMessage = $state('');

	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const keyId = id;
		const controller = new AbortController();
		notFound = false;
		getNSSKey(keyId, controller.signal)
			.then((result) => {
				key = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				notFound = cause instanceof ApiError && cause.isNotFound;
				loadError = notFound ? null : errorMessage(cause);
				hasLoaded = true;
			});
		return () => controller.abort();
	});

	const status = $derived(key ? keyStatus(key, now) : NSSKeyStatusActive);
	const revoked = $derived(key?.status === NSSKeyStatusRevoked);
	const mayRotate = $derived(!!key && !revoked && can(key, NSSKeyPermissionRotate));
	const mayRevoke = $derived(!!key && !revoked && can(key, NSSKeyPermissionRevoke));
	const mayEdit = $derived(!!key && !revoked && can(key, NSSKeyPermissionEdit));
	const mayAssign = $derived(!!key && !revoked && can(key, NSSKeyPermissionAssignGroup));
	const graceLive = $derived(!!key && previousLive(key, now));

	// ---- Inline edits ---------------------------------------------------

	// The expiry is not here: it is fixed when the key is created.
	type Field = 'description' | 'group' | 'email';
	let editing = $state<Field | null>(null);
	let draft = $state('');
	let saving = $state(false);
	let editError = $state<string | null>(null);

	const fieldNames: Record<Field, string> = {
		description: 'Description',
		group: 'Group',
		email: 'Notification email'
	};

	/** startEdit opens one field for editing, seeded with its stored value. */
	function startEdit(field: Field) {
		if (!key) return;
		editing = field;
		editError = null;
		draft =
			field === 'description'
				? key.description
				: field === 'group'
					? key.group_name
					: key.notification_email;
	}

	function cancelEdit() {
		editing = null;
		editError = null;
	}

	/** saveEdit sends the one field being edited, and only it. */
	async function saveEdit(event: SubmitEvent) {
		event.preventDefault();
		if (!key || !editing) return;
		const field = editing;
		const body: UpdateNSSKeyBody = {};
		if (field === 'description') body.description = draft.trim();
		if (field === 'group') body.group_name = draft.trim();
		if (field === 'email') body.notification_email = draft.trim();
		saving = true;
		editError = null;
		try {
			key = await updateNSSKey(key.id, body);
			editing = null;
			liveMessage = `${fieldNames[field]} saved.`;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			editError = errorMessage(cause);
		} finally {
			saving = false;
		}
	}

	// ---- Usage alerts ---------------------------------------------------

	// The miss rate and key source alerts that fired this week. The usage
	// table's rows are daily counts and the miss rate alert is a per-minute
	// rate, so the alert is stated here rather than as a highlighted row.
	const alerts = $derived(key ? keyAlerts(key) : []);

	// ---- Rotate ---------------------------------------------------------

	// The server's rotation grace, so the dialog can name the day the
	// current secret stops working. A failed read only loses the date: the
	// dialog then speaks of the grace period without one.
	let settings = $state<NSSKeySettings | null>(null);
	$effect(() => {
		const controller = new AbortController();
		getNSSKeySettings(controller.signal)
			.then((result) => (settings = result))
			.catch(() => {});
		return () => controller.abort();
	});
	const graceUntil = $derived(
		settings ? graceEnd(now, settings.rotation_grace_seconds).toISOString() : null
	);

	// The expiry the rotation will give the key: undefined until the
	// server's limit is known, null when the key will still never expire.
	const renewed = $derived(
		key && settings ? renewedExpiry(key, now, settings.max_lifetime_seconds) : undefined
	);

	let confirmRotate = $state(false);
	let rotating = $state(false);
	let rotateError = $state<string | null>(null);
	// The rotation's answer while its secret is on screen; dropped on Done.
	let issued = $state<NSSKeySecret | null>(null);

	async function rotate() {
		if (!key) return;
		rotating = true;
		rotateError = null;
		try {
			const result = await rotateNSSKey(key.id);
			key = result.key;
			issued = result;
			confirmRotate = false;
			liveMessage = 'Key rotated. Store the new secret.';
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			rotateError = errorMessage(cause);
		} finally {
			rotating = false;
		}
	}

	function dismissSecret() {
		issued = null;
	}

	beforeNavigate(({ cancel, type }) => {
		if (!issued) return;
		if (type === 'leave') {
			cancel();
			return;
		}
		if (!confirm('Leave without storing the new secret? It cannot be shown again.')) {
			cancel();
		}
	});

	// ---- Clear the previous secret -------------------------------------

	let confirmClear = $state(false);
	let clearing = $state(false);
	let clearError = $state<string | null>(null);
	// The rotation report's not-switched count, read when the dialog opens.
	let notSwitched = $state<number | null>(null);
	let totalHosts = $state<number | null>(null);
	let countError = $state(false);

	function openClear() {
		if (!key) return;
		confirmClear = true;
		clearError = null;
		notSwitched = null;
		totalHosts = null;
		countError = false;
		getNSSRotationReport(key.id)
			.then((report) => {
				notSwitched = report.not_switched;
				totalHosts = report.switched + report.not_switched + report.gone;
			})
			.catch((cause) => {
				if (redirectIfUnauthenticated(cause)) return;
				countError = true;
			});
	}

	async function clearPrevious() {
		if (!key) return;
		clearing = true;
		clearError = null;
		try {
			key = await clearNSSKeyPrevious(key.id);
			confirmClear = false;
			liveMessage = 'The previous secret is no longer accepted.';
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			clearError = errorMessage(cause);
		} finally {
			clearing = false;
		}
	}

	// ---- Revoke ---------------------------------------------------------

	let confirmRevoke = $state(false);
	let revoking = $state(false);
	let revokeError = $state<string | null>(null);

	async function revoke() {
		if (!key) return;
		revoking = true;
		revokeError = null;
		try {
			key = await revokeNSSKey(key.id);
			confirmRevoke = false;
			// A revoked key can no longer be rotated; a secret still on
			// screen would only mislead.
			issued = null;
			liveMessage = 'Key revoked.';
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			revokeError = errorMessage(cause);
		} finally {
			revoking = false;
		}
	}

	// ---- Usage ----------------------------------------------------------

	let usage = $state<NSSKeyUsageRow[]>([]);
	let usageTotal = $state(0);
	let usageCursor = $state<string | undefined>(undefined);
	let usageLoaded = $state(false);
	let usageError = $state<string | null>(null);
	let usageAttempt = $state(0);
	let moreBusy = $state(false);
	let moreError = $state<string | null>(null);

	$effect(() => {
		void usageAttempt;
		const keyId = id;
		const controller = new AbortController();
		getNSSKeyUsage(keyId, { signal: controller.signal })
			.then((result) => {
				usage = result.rows;
				usageTotal = result.total;
				usageCursor = result.next_cursor;
				usageLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				usageError = errorMessage(cause);
				usageLoaded = true;
			});
		return () => controller.abort();
	});

	function retryUsage() {
		usageError = null;
		usageLoaded = false;
		usageAttempt += 1;
	}

	async function loadMoreUsage() {
		if (!usageCursor) return;
		moreBusy = true;
		moreError = null;
		try {
			const result = await getNSSKeyUsage(id, { after: usageCursor });
			usage = [...usage, ...result.rows];
			usageTotal = result.total;
			usageCursor = result.next_cursor;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			moreError = errorMessage(cause);
		} finally {
			moreBusy = false;
		}
	}

	/** utcDay is a usage row's day, which the server counts in UTC. */
	function utcDay(value: string): string {
		const at = new Date(value);
		if (Number.isNaN(at.getTime())) return '—';
		return `${at.toLocaleDateString(undefined, { timeZone: 'UTC', year: 'numeric', month: 'short', day: 'numeric' })} UTC`;
	}

	/** timeOfDay is the clock time of a timestamp, naming the zone. */
	function timeOfDay(value: string): string {
		const at = new Date(value);
		if (Number.isNaN(at.getTime())) return '—';
		return at.toLocaleTimeString(undefined, {
			hour: 'numeric',
			minute: '2-digit',
			timeZoneName: 'short'
		});
	}

	const expiryClass = $derived.by(() => {
		if (!key || revoked) return 'text-ink';
		const tone = expiryTone(key.expires_at, now);
		return tone === 'past' ? 'text-danger' : tone === 'soon' ? 'text-trimmed' : 'text-ink';
	});
</script>

{#snippet editButton(field: Field)}
	<button
		type="button"
		onclick={() => startEdit(field)}
		data-testid="nss-key-edit-{field}"
		class="inline-flex min-h-target items-center gap-1 rounded-md px-2 text-xs text-ink-muted transition hover:bg-surface-muted hover:text-ink"
	>
		<Icon name="pencil" size="xs" />
		Edit<span class="sr-only"> {fieldNames[field].toLowerCase()}</span>
	</button>
{/snippet}

{#snippet editForm(field: Field, type: 'text' | 'email' | 'textarea')}
	<form class="flex w-full flex-col gap-2" onsubmit={saveEdit} data-testid="nss-key-form-{field}">
		{#if type === 'textarea'}
			<textarea
				bind:value={draft}
				rows="2"
				aria-label={fieldNames[field]}
				disabled={saving}
				data-testid="nss-key-input-{field}"
				class="w-full max-w-[70ch] rounded-md border border-border-control bg-surface px-3 py-2 text-sm"
			></textarea>
		{:else}
			<input
				bind:value={draft}
				{type}
				aria-label={fieldNames[field]}
				disabled={saving}
				data-testid="nss-key-input-{field}"
				class="min-h-target w-full max-w-[420px] rounded-md border border-border-control bg-surface px-3 py-2 text-sm {field ===
				'group'
					? 'font-mono'
					: ''}"
			/>
		{/if}
		{#if editError}
			<span role="alert" class="text-xs text-danger" data-testid="nss-key-edit-error"
				>Not saved: {editError}</span
			>
		{/if}
		<div class="flex gap-2">
			<Button type="submit" busy={saving} testid="nss-key-save-{field}"
				>{saving ? 'Saving…' : 'Save'}</Button
			>
			<Button variant="ghost" disabled={saving} onclick={cancelEdit}>Cancel</Button>
		</div>
	</form>
{/snippet}

<svelte:head><title>{key ? `${key.name} · ` : ''}NSS key · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading
		title="NSS key"
		testid="nss-key-heading"
		back={{ href: resolve('/nss-keys'), label: 'All NSS keys', testid: 'nss-key-back' }}
	>
		{#snippet action()}
			{#if mayRevoke}
				<Button
					variant="danger"
					onclick={() => {
						revokeError = null;
						confirmRevoke = true;
					}}
					testid="nss-key-revoke">Revoke key</Button
				>
			{/if}
		{/snippet}
	</PageHeading>

	<div aria-live="polite" aria-atomic="true" class="sr-only" data-testid="nss-key-live">
		{liveMessage}
	</div>

	{#if notFound}
		<Alert variant="error" title="No such NSS key" testid="nss-key-missing">
			No key with this id is shared with you. It may never have existed, or it belongs to a group
			you are not in.
		</Alert>
	{:else if loadError}
		<Alert variant="error" title="Could not load this key" onretry={retry} testid="nss-key-error"
			>{loadError}</Alert
		>
	{:else if !hasLoaded || !key}
		<LoadingBlock shape="lines" count={6} label="Loading this key…" testid="nss-key-loading" />
	{:else}
		{#if issued}
			<SecretPanel
				keyId={issued.key.id}
				secret={issued.secret}
				previousExpiresAt={issued.key.previous_expires_at}
				ondismiss={dismissSecret}
				testid="secret-panel"
			/>
		{/if}

		<!-- Identity strip: the key as the page's subject. -->
		<div class="flex flex-col gap-2 rounded-lg bg-surface p-5" data-testid="nss-key-identity">
			<div class="flex flex-wrap items-start justify-between gap-3">
				<div class="flex min-w-0 flex-wrap items-center gap-3">
					<KeyStatusBadge {status} testid="nss-key-status" />
					<span class="text-lg font-semibold break-words text-ink" data-testid="nss-key-name"
						>{key.name}</span
					>
				</div>
				<CopyableId value={key.id} label="Key id" length={key.id.length} testid="nss-key-id" />
			</div>
			{#if !mayRotate && !mayRevoke && !mayEdit && !revoked}
				<p class="text-dense text-ink-muted" data-testid="nss-key-readonly">
					You can read this key and its usage. Changing it needs the fleet key admin role or
					membership of its group.
				</p>
			{/if}
		</div>

		{#if revoked}
			<Alert variant="info" title="This key is revoked" testid="nss-key-revoked">
				Neither secret is accepted any more{#if key.revoked_at}, since {formatDateTime(
						key.revoked_at
					)}{/if}{#if key.revoked_by_username}, revoked by
					<span class="font-mono">{key.revoked_by_username}</span>{/if}. Hosts that still present it
				resolve no directory accounts.
			</Alert>
		{/if}

		{#if alerts.length > 0}
			<Alert variant="warning" title="Unusual use in the last 7 days" testid="nss-key-alerts">
				<div class="flex flex-col gap-2">
					{#each alerts as alert (alert.kind)}
						{#if alert.kind === 'misses'}
							<p data-testid="nss-key-alert-misses">
								Many unknown names were looked up with this key at {formatDateTime(alert.at)}. A
								host may be asking for names the directory does not hold, or someone may be guessing
								account names; the usage below shows which address.
							</p>
						{:else}
							<p data-testid="nss-key-alert-sources">
								More addresses used this key in one day than the server expects, at {formatDateTime(
									alert.at
								)}. If the key has spread beyond its fleet, rotate it.
							</p>
						{/if}
					{/each}
				</div>
			</Alert>
		{/if}

		<PageSection
			title="Secret"
			description="Hosts present the current secret. After a rotation the previous one is still accepted for a grace period, so hosts can switch over."
			testid="nss-key-secret"
		>
			<dl class="flex max-w-[70ch] flex-col divide-y divide-border-subtle">
				<DetailRow label="Generation">
					<span data-testid="nss-key-generation">{key.current_generation}</span>
				</DetailRow>
				<DetailRow label="Last rotated">
					{key.rotated_at ? formatDateTime(key.rotated_at) : 'Never'}
				</DetailRow>
				<DetailRow label="Previous secret">
					{#if graceLive && key.previous_expires_at}
						<span class="text-trimmed" data-testid="nss-key-previous">
							Accepted until {formatDateTime(key.previous_expires_at)} ({remainingLabel(
								key.previous_expires_at,
								now
							)})
						</span>
					{:else}
						<span class="text-ink-muted" data-testid="nss-key-previous">None accepted</span>
					{/if}
				</DetailRow>
			</dl>

			<div class="mt-4 flex flex-wrap items-center gap-3">
				{#if mayRotate}
					<Button
						onclick={() => {
							rotateError = null;
							confirmRotate = true;
						}}
						testid="nss-key-rotate">Rotate secret</Button
					>
				{/if}
				{#if mayRotate && graceLive}
					<Button variant="ghost" onclick={openClear} testid="nss-key-clear-previous">
						End the previous secret now
					</Button>
				{/if}
				{#if key.rotated_at}
					<a
						href={resolve('/nss-keys/[id]/rotation', { id: key.id })}
						class="inline-flex min-h-target items-center gap-1 text-sm text-accent hover:underline"
						data-testid="nss-key-rotation-link"
					>
						Rotation report
						<Icon name="chevron-right" size="xs" />
					</a>
				{/if}
			</div>
		</PageSection>

		<PageSection title="Details" testid="nss-key-details">
			<dl class="flex max-w-[70ch] flex-col divide-y divide-border-subtle">
				<DetailRow label="Description">
					{#if editing === 'description'}
						{@render editForm('description', 'textarea')}
					{:else}
						<span class="inline-flex flex-wrap items-center gap-2">
							<span class={key.description ? 'text-ink' : 'text-ink-muted'}
								>{key.description || 'None'}</span
							>
							{#if mayEdit}{@render editButton('description')}{/if}
						</span>
					{/if}
				</DetailRow>
				<DetailRow label="Group">
					{#if editing === 'group'}
						{@render editForm('group', 'text')}
					{:else}
						<span class="inline-flex flex-wrap items-center gap-2">
							{#if key.group_name}
								<span class="font-mono" data-testid="nss-key-group">{key.group_name}</span>
							{:else}
								<span class="text-ink-muted" data-testid="nss-key-group">None</span>
							{/if}
							{#if mayAssign}{@render editButton('group')}{/if}
						</span>
					{/if}
				</DetailRow>
				<DetailRow label="Expires">
					<span class={expiryClass} data-testid="nss-key-expires">
						{#if key.expires_at}
							{formatDateTime(key.expires_at)} ({remainingLabel(key.expires_at, now)})
						{:else}
							Never
						{/if}
					</span>
				</DetailRow>
				<DetailRow label="Notification email">
					{#if editing === 'email'}
						{@render editForm('email', 'email')}
					{:else}
						<span class="inline-flex flex-wrap items-center gap-2">
							<span class={key.notification_email ? 'text-ink' : 'text-ink-muted'}
								>{key.notification_email || 'Nowhere'}</span
							>
							{#if mayEdit}{@render editButton('email')}{/if}
						</span>
					{/if}
				</DetailRow>
				<DetailRow label="Created">
					{formatDateTime(key.created_at)}
					{#if key.created_by_username}by <span class="font-mono">{key.created_by_username}</span
						>{/if}
				</DetailRow>
			</dl>
		</PageSection>

		<PageSection
			title="Usage"
			description="Lookups per source address, secret generation and UTC day, newest first. Counts are written once a minute."
			testid="nss-key-usage"
		>
			{#if usageError}
				<Alert
					variant="error"
					title="Could not load this key's usage"
					onretry={retryUsage}
					testid="nss-key-usage-error">{usageError}</Alert
				>
			{:else if !usageLoaded}
				<LoadingBlock shape="table" count={3} testid="nss-key-usage-loading" />
			{:else if usage.length === 0}
				<p class="text-dense text-ink-muted" data-testid="nss-key-usage-empty">
					No lookups yet. A host's first lookup with this key shows here within a minute.
				</p>
			{:else}
				<div class="flex flex-col gap-3">
					<div class="overflow-x-auto">
						<table class="data-table" data-testid="nss-key-usage-table">
							<thead>
								<tr>
									<th scope="col">Day</th>
									<th scope="col">Source address</th>
									<th scope="col">Secret</th>
									<th scope="col" class="text-right">Lookups</th>
									<th scope="col" class="text-right">Misses</th>
									<th scope="col" class="max-sm:hidden">Seen</th>
								</tr>
							</thead>
							<tbody>
								{#each usage as row (`${row.day}|${row.source_ip}|${row.generation}`)}
									<tr data-testid="nss-key-usage-row">
										<td class="whitespace-nowrap">{utcDay(row.day)}</td>
										<td class="font-mono">{row.source_ip}</td>
										<td class="whitespace-nowrap">
											Generation {row.generation}
											{#if row.generation === key.current_generation}
												<span class="text-ink-muted">(current)</span>
											{:else}
												<span class="text-trimmed">(older)</span>
											{/if}
										</td>
										<td class="text-right">{row.lookups.toLocaleString()}</td>
										<td class="text-right whitespace-nowrap" data-testid="nss-key-usage-misses">
											{row.misses.toLocaleString()}
											<span class="text-ink-muted"
												>({formatPercent(missRate(row.lookups, row.misses))})</span
											>
										</td>
										<td class="whitespace-nowrap text-ink-muted max-sm:hidden">
											{timeOfDay(row.first_seen)} to {timeOfDay(row.last_seen)}
										</td>
									</tr>
								{/each}
							</tbody>
						</table>
					</div>
					<LoadMoreFooter
						summary={describeLoaded(usage.length, usageTotal - usage.length, 'older')}
						hasMore={!!usageCursor}
						busy={moreBusy}
						error={moreError}
						errorTitle="Could not load more usage"
						onmore={loadMoreUsage}
						testid="nss-key-usage"
					/>
				</div>
			{/if}
		</PageSection>
	{/if}
</PageShell>

{#if confirmRotate && key}
	<ConfirmModal
		title="Rotate this key?"
		reasonless
		variant="primary"
		confirmLabel="Rotate secret"
		busyLabel="Rotating…"
		busy={rotating}
		error={rotateError}
		errorTitle="The key was not rotated"
		confirmTestid="nss-key-rotate-confirm"
		onconfirm={rotate}
		oncancel={() => (confirmRotate = false)}
	>
		<p data-testid="nss-key-rotate-grace">
			A new secret is issued and shown once.
			{#if key.status === NSSKeyStatusExpired}
				<span class="font-semibold text-trimmed">The current secret is not accepted again:</span>
				the key has expired, so there is no grace period and every host fails until it has the new secret.
			{:else if settings && graceUntil && settings.rotation_grace_seconds > 0}
				The current secret becomes the previous one and is still accepted until
				<span class="font-semibold text-ink">{formatDateTime(graceUntil)}</span>
				({formatDuration(settings.rotation_grace_seconds)} from now), so hosts can switch over; the rotation
				report shows which have.
			{:else if settings}
				<span class="font-semibold text-trimmed">The current secret stops working at once:</span>
				this server keeps no grace period, so every host fails until it has the new secret.
			{:else}
				The current secret becomes the previous one and is still accepted for the rotation grace
				period, so hosts can switch over; the rotation report shows which have.
			{/if}
		</p>
		<p class="mt-2" data-testid="nss-key-rotate-expiry">
			{#if renewed === undefined}
				The key's expiry is renewed by its lifetime, up to the server's limit.
			{:else if renewed === null}
				The key still never expires.
			{:else}
				The key's expiry is renewed to
				<span class="font-semibold text-ink">{formatDateTime(renewed.toISOString())}</span>.
			{/if}
		</p>
		{#if graceLive && key.previous_expires_at}
			<p class="mt-2 text-trimmed" data-testid="nss-key-rotate-replaces">
				The secret now accepted as previous, until {formatDateTime(key.previous_expires_at)}, stops
				working at once. Hosts still on it stop resolving accounts.
			</p>
		{/if}
	</ConfirmModal>
{/if}

{#if confirmClear && key}
	<ConfirmModal
		title="End the previous secret now?"
		reasonless
		confirmLabel="End previous secret"
		busyLabel="Ending…"
		busy={clearing}
		error={clearError}
		errorTitle="The previous secret was not ended"
		confirmTestid="nss-key-clear-confirm"
		onconfirm={clearPrevious}
		oncancel={() => (confirmClear = false)}
	>
		<p data-testid="nss-key-clear-count">
			{#if countError}
				Could not check which hosts still present the old secret. Open the rotation report before
				you go on.
			{:else if notSwitched === null}
				Checking which hosts still present the old secret…
			{:else if notSwitched === 0}
				Every host seen since the rotation has switched to the new secret.
			{:else}
				<span class="font-semibold text-danger"
					>{notSwitched}
					{notSwitched === 1 ? 'host' : 'hosts'} still on the old secret will start failing.</span
				>
				{#if totalHosts}That is {notSwitched} of {totalHosts} hosts seen.{/if}
			{/if}
		</p>
		<p class="mt-2">
			The previous secret stops being accepted now rather than at the end of its grace period.
		</p>
	</ConfirmModal>
{/if}

{#if confirmRevoke && key}
	<ConfirmModal
		title="Revoke this key?"
		reasonless
		confirmText={key.id}
		confirmTextNoun="the key id"
		confirmTextTestid="nss-key-revoke-typed"
		confirmLabel="Revoke key"
		busyLabel="Revoking…"
		busy={revoking}
		error={revokeError}
		errorTitle="The key was not revoked"
		confirmTestid="nss-key-revoke-confirm"
		onconfirm={revoke}
		oncancel={() => (confirmRevoke = false)}
	>
		Both the current and the previous secret stop being accepted now. Every host using
		<span class="font-mono">{key.name}</span> stops resolving directory accounts until it gets a new key.
		This cannot be undone.
	</ConfirmModal>
{/if}
