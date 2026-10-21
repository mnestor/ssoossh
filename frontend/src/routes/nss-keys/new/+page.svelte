<script lang="ts">
	import { beforeNavigate, goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';

	import { ApiError } from '#lib/api/client.js';
	import { createNSSKey, getNSSKeySettings } from '#lib/api/endpoints.js';
	import type { NSSKeySecret } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import AccessDenied from '#lib/components/AccessDenied.svelte';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import KeyGroupSuggestions from '#lib/components/KeyGroupSuggestions.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SecretPanel from '#lib/components/SecretPanel.svelte';
	import { formatDateTime, formatDuration } from '#lib/format.js';
	import { endOfDay, latestExpiryDay, localDay } from '#lib/nss.js';
	import { nssAccess } from '#lib/nssAccess.svelte.js';
	import { session } from '#lib/session.svelte.js';

	// Creating a fleet key: one short form, then the secret, once.
	//
	// The form asks only what the server needs and what an operator would
	// otherwise come back to set: a name, what the key is for, the group that
	// may manage it, when it ends and where its mail goes. Everything but the
	// name is optional, and the two with a server-side default start at it:
	// the expiry at the last day nss.key_max_lifetime allows, read from the
	// server, and the address at the reader's own. An empty expiry still
	// takes the server's default, so a failed read costs only the proposal
	// and the bound, which the server enforces anyway. Once the key exists
	// only a rotation moves the expiry, renewing it by the same length, so
	// the form says so.

	const allowed = $derived(session.user?.is_nss_key_admin ?? false);

	// Opened from the admin list (?from=admin), the back chip and Done
	// return there; otherwise to the keys the reader's groups manage. The
	// value is only looked up, never followed.
	const fromAdmin = $derived(page.url.searchParams.get('from') === 'admin');
	const back = $derived(
		fromAdmin
			? { href: resolve('/admin/nss/keys'), label: 'All NSS keys' }
			: { href: resolve('/nss-keys'), label: 'NSS keys' }
	);

	let name = $state('');
	let description = $state('');
	let group = $state('');
	let expires = $state('');
	let email = $state(session.user?.email ?? '');

	let nameError = $state(false);
	// Which bound a typed expiry broke: the field's min and max are advice
	// a typed date can pass, so both are checked before the server is asked.
	let expiresError = $state<'early' | 'late' | null>(null);
	let busy = $state(false);
	let submitError = $state<string | null>(null);
	let nameInput = $state<HTMLInputElement>();
	let expiresInput = $state<HTMLInputElement>();

	// The answer while its secret is on screen. Dropped on Done, so nothing
	// on the page holds the secret afterwards.
	let issued = $state<NSSKeySecret | null>(null);
	// The expiry the form sent, so the answer can say when the server's
	// limit trimmed it to an earlier one.
	let requestedExpiry = $state<string | undefined>(undefined);
	const trimmed = $derived(
		!!issued?.key.expires_at &&
			!!requestedExpiry &&
			new Date(issued.key.expires_at).getTime() < new Date(requestedExpiry).getTime() - 1000
	);
	// The panel's "I have stored this secret" tick, so leaving stops
	// warning once the reader has said the secret is safe.
	let secretStored = $state(false);
	// What a screen reader hears once the key exists: the sub line changes
	// too, but it is not a live region.
	let liveMessage = $state('');

	// The earliest expiry the date field offers: tomorrow, since the server
	// refuses a time already past.
	const minDate = $derived(localDay(new Date(Date.now() + 24 * 60 * 60 * 1000)));

	// The server's nss.key_max_lifetime once read: null until then or when
	// the read fails, 0 when the server sets no limit. The last day it
	// allows bounds the field and is proposed; a day the reader typed first
	// is never overwritten. A limit under a day leaves no day to pick, so
	// the key takes the limit itself.
	let maxLifetime = $state<number | null>(null);
	const maxDate = $derived(maxLifetime ? latestExpiryDay(new Date(), maxLifetime) : null);
	const noDay = $derived(maxDate !== null && maxDate < minDate);
	$effect(() => {
		if (!allowed) return;
		const controller = new AbortController();
		getNSSKeySettings(controller.signal)
			.then((result) => {
				maxLifetime = result.max_lifetime_seconds;
				const day = latestExpiryDay(new Date(), result.max_lifetime_seconds);
				if (day && day >= minDate && expires === '') expires = day;
			})
			.catch(() => {});
		return () => controller.abort();
	});

	// A key whose secret has not been stored yet is lost by leaving, so
	// leaving asks first. Closing the tab gets the browser's own prompt.
	beforeNavigate(({ cancel, type }) => {
		if (!issued || secretStored) return;
		if (type === 'leave') {
			cancel();
			return;
		}
		if (!confirm('Leave without storing the secret? It cannot be shown again.')) {
			cancel();
		}
	});

	async function submit(event: SubmitEvent) {
		event.preventDefault();
		if (name.trim() === '') {
			nameError = true;
			nameInput?.focus();
			return;
		}
		nameError = false;
		// The field's min and max are advice a typed date can pass, so they
		// are checked here too rather than left for the server to refuse or
		// trim.
		if (expires && !noDay && expires < minDate) {
			expiresError = 'early';
			expiresInput?.focus();
			return;
		}
		if (expires && maxDate && expires > maxDate && !noDay) {
			expiresError = 'late';
			expiresInput?.focus();
			return;
		}
		expiresError = null;
		busy = true;
		submitError = null;
		try {
			secretStored = false;
			const expiresAt = expires && !noDay ? endOfDay(expires).toISOString() : undefined;
			requestedExpiry = expiresAt;
			issued = await createNSSKey({
				name: name.trim(),
				description: description.trim() || undefined,
				group_name: group.trim() || undefined,
				expires_at: expiresAt,
				notification_email: email.trim()
			});
			const expiry = issued.key.expires_at
				? `It expires ${formatDateTime(issued.key.expires_at)}.`
				: 'It never expires.';
			liveMessage = `Key ${issued.key.name} created. ${expiry} Store its secret now.`;
			// A key in one of the reader's groups may be their first, which
			// is what shows the rail's NSS keys entry.
			nssAccess.refresh(session.user);
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			submitError =
				cause instanceof ApiError && cause.isForbidden
					? 'Creating keys needs the fleet key admin role.'
					: errorMessage(cause);
		} finally {
			busy = false;
		}
	}

	// Done: forget the secret, then open the key it belongs to.
	function dismiss() {
		const id = issued?.key.id ?? '';
		issued = null;
		// A resolve()'d route with a query appended, not a bare route id.
		// eslint-disable-next-line svelte/no-navigation-without-resolve
		goto(`${resolve('/nss-keys/[id]', { id: id })}${fromAdmin ? '?from=admin' : ''}`);
	}

	const field =
		'min-h-target w-full rounded-md border border-border-control bg-surface px-3 py-2 text-sm text-ink';
</script>

<svelte:head><title>New NSS key · ssoossh</title></svelte:head>

{#if !allowed}
	<AccessDenied
		requirement="Creating NSS keys needs the fleet key admin role. Ask an administrator if you need it."
		testid="nss-key-new-denied"
	/>
{:else}
	<PageShell width="wide">
		<PageHeading
			title="New NSS key"
			back={{ href: back.href, label: back.label, testid: 'nss-key-new-back' }}
		>
			{#snippet sub()}
				{#if issued}
					<span data-testid="nss-key-created">
						Created <span class="font-mono text-ink">{issued.key.name}</span>.
						<span data-testid="nss-key-created-expiry"
							>{#if issued.key.expires_at && trimmed}It expires {formatDateTime(
									issued.key.expires_at
								)}, the latest the server's limit allows, not the day you chose.{:else if issued.key.expires_at}It
								expires {formatDateTime(issued.key.expires_at)}.{:else}It never expires.{/if}</span
						>
						Store its secret before you leave this page.
					</span>
				{:else}
					One key per fleet of hosts. The secret is shown once, when you create it.
				{/if}
			{/snippet}
		</PageHeading>

		<div aria-live="polite" aria-atomic="true" class="sr-only" data-testid="nss-key-new-live">
			{liveMessage}
		</div>

		{#if issued}
			<SecretPanel
				bind:stored={secretStored}
				keyId={issued.key.id}
				secret={issued.secret}
				ondismiss={dismiss}
				testid="secret-panel"
			/>
		{:else}
			<PageSection title="The key" testid="nss-key-form-section">
				<form
					class="flex max-w-[70ch] flex-col gap-5"
					onsubmit={submit}
					novalidate
					data-testid="nss-key-form"
				>
					<!-- The error sits outside the label, so it is the field's
					     description only and not read again as part of its name. -->
					<div class="flex flex-col gap-1">
						<label for="nss-key-name-input" class="text-sm font-medium text-ink">Name</label>
						<input
							bind:this={nameInput}
							bind:value={name}
							id="nss-key-name-input"
							type="text"
							autocomplete="off"
							aria-invalid={nameError}
							aria-describedby={nameError ? 'nss-key-name-error' : undefined}
							data-testid="nss-key-name"
							class={field}
						/>
						{#if nameError}
							<span
								id="nss-key-name-error"
								class="text-xs text-danger"
								data-testid="nss-key-name-error"
								>Give the key a name, such as the fleet it serves.</span
							>
						{/if}
					</div>

					<label class="flex flex-col gap-1">
						<span class="text-sm font-medium text-ink">
							Description <span class="font-normal text-ink-muted">(optional)</span>
						</span>
						<textarea
							bind:value={description}
							rows="2"
							data-testid="nss-key-description"
							class="{field} min-h-0"></textarea>
					</label>

					<label class="flex flex-col gap-1">
						<span class="text-sm font-medium text-ink">
							Group <span class="font-normal text-ink-muted">(optional)</span>
						</span>
						<input
							bind:value={group}
							type="text"
							list="nss-key-groups"
							autocomplete="off"
							data-testid="nss-key-group"
							class="{field} max-w-[420px] font-mono"
						/>
						<!-- The groups other keys use and the reader's own: suggestions,
						     never a limit, since the server takes any group name. -->
						<KeyGroupSuggestions id="nss-key-groups" />
						<span class="text-xs text-ink-muted">
							Members of this group can rotate, edit and revoke the key without an admin role. Any
							group name is accepted; the suggestions are the groups other keys use and the groups
							you are in.
						</span>
					</label>

					<label class="flex flex-col gap-1">
						<span class="text-sm font-medium text-ink">
							Expires <span class="font-normal text-ink-muted">(optional)</span>
						</span>
						<input
							bind:this={expiresInput}
							bind:value={expires}
							type="date"
							min={minDate}
							max={maxDate ?? undefined}
							disabled={noDay}
							aria-invalid={!!expiresError}
							aria-describedby={expiresError ? 'nss-key-expires-error' : 'nss-key-expires-hint'}
							data-testid="nss-key-expires"
							class="{field} max-w-[420px]"
						/>
						{#if expiresError === 'early'}
							<span
								id="nss-key-expires-error"
								class="text-xs text-danger"
								data-testid="nss-key-expires-error">Choose a day after today.</span
							>
						{:else if expiresError === 'late' && maxLifetime}
							<span
								id="nss-key-expires-error"
								class="text-xs text-danger"
								data-testid="nss-key-expires-error"
								>Choose a day inside the server's limit of {formatDuration(maxLifetime)}.</span
							>
						{/if}
						<span
							id="nss-key-expires-hint"
							class="text-xs text-ink-muted"
							data-testid="nss-key-expires-hint"
						>
							{#if noDay && maxLifetime}
								The server limits keys to {formatDuration(maxLifetime)}, so this key expires that
								long from now.
							{:else if maxLifetime}
								At the end of that day. The server limits keys to {formatDuration(maxLifetime)}.
							{:else if maxLifetime === 0}
								At the end of that day. Leave it empty and the key never expires.
							{:else}
								At the end of that day. Leave it empty for the server's default.
							{/if}
							The expiry is set now. Each rotation renews it by the same length, up to the server's limit.
						</span>
					</label>

					<label class="flex flex-col gap-1">
						<span class="text-sm font-medium text-ink">
							Notification email <span class="font-normal text-ink-muted">(optional)</span>
						</span>
						<input
							bind:value={email}
							type="email"
							autocomplete="email"
							data-testid="nss-key-email"
							class="{field} max-w-[420px]"
						/>
						<span class="text-xs text-ink-muted">
							Where expiry reminders and alerts about this key go. Empty sends them nowhere.
						</span>
					</label>

					{#if submitError}
						<Alert variant="error" title="The key was not created" testid="nss-key-create-error">
							{submitError}
						</Alert>
					{/if}

					<div class="flex flex-wrap items-center gap-3">
						<Button type="submit" {busy} testid="nss-key-create">
							{busy ? 'Creating…' : 'Create key'}
						</Button>
						<a
							href={resolve('/nss-keys')}
							class="inline-flex min-h-target items-center text-sm text-accent hover:underline"
							>Cancel</a
						>
					</div>
				</form>
			</PageSection>
		{/if}
	</PageShell>
{/if}
