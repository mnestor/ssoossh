<script lang="ts">
	import { beforeNavigate, goto } from '$app/navigation';
	import { resolve } from '$app/paths';

	import { ApiError } from '#lib/api/client.js';
	import { createNSSKey, getNSSKeySettings, listNSSKeys } from '#lib/api/endpoints.js';
	import type { NSSKeySecret } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import AccessDenied from '#lib/components/AccessDenied.svelte';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SecretPanel from '#lib/components/SecretPanel.svelte';
	import { formatDuration } from '#lib/format.js';
	import { endOfDay, latestExpiryDay, localDay } from '#lib/nss.js';
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
	// and the bound, which the server enforces anyway. The expiry is fixed
	// once the key exists, so the form says so.

	const allowed = $derived(session.user?.is_nss_key_admin ?? false);

	let name = $state('');
	let description = $state('');
	let group = $state('');
	let expires = $state('');
	let email = $state(session.user?.email ?? '');

	let nameError = $state(false);
	let expiresError = $state(false);
	let busy = $state(false);
	let submitError = $state<string | null>(null);
	let nameInput = $state<HTMLInputElement>();
	let expiresInput = $state<HTMLInputElement>();

	// The answer while its secret is on screen. Dropped on Done, so nothing
	// on the page holds the secret afterwards.
	let issued = $state<NSSKeySecret | null>(null);

	// Groups other keys already use, and the reader's own, offered as
	// suggestions; any name may still be typed. A failed read only loses
	// the suggestions.
	let knownGroups = $state<string[]>([]);
	$effect(() => {
		if (!allowed) return;
		const controller = new AbortController();
		listNSSKeys(controller.signal)
			.then((result) => {
				knownGroups = result.keys.map((key) => key.group_name);
			})
			.catch(() => {});
		return () => controller.abort();
	});
	const suggestions = $derived(
		[...new Set([...knownGroups, ...(session.user?.groups ?? [])])]
			.filter((value) => value !== '')
			.sort()
	);

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
		if (!issued) return;
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
		// The field's max is advice a typed date can pass, so it is checked
		// here too rather than left for the server to trim.
		if (expires && maxDate && expires > maxDate && !noDay) {
			expiresError = true;
			expiresInput?.focus();
			return;
		}
		expiresError = false;
		busy = true;
		submitError = null;
		try {
			issued = await createNSSKey({
				name: name.trim(),
				description: description.trim() || undefined,
				group_name: group.trim() || undefined,
				expires_at: expires && !noDay ? endOfDay(expires).toISOString() : undefined,
				notification_email: email.trim()
			});
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
		goto(resolve('/nss-keys/[id]', { id: id }));
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
			back={{ href: resolve('/nss-keys'), label: 'All NSS keys', testid: 'nss-key-new-back' }}
		>
			{#snippet sub()}
				{#if issued}
					<span data-testid="nss-key-created">
						Created <span class="font-mono text-ink">{issued.key.name}</span>. Store its secret
						before you leave this page.
					</span>
				{:else}
					One key per fleet of hosts. The secret is shown once, when you create it.
				{/if}
			{/snippet}
		</PageHeading>

		{#if issued}
			<SecretPanel
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
					<label class="flex flex-col gap-1">
						<span class="text-sm font-medium text-ink">Name</span>
						<input
							bind:this={nameInput}
							bind:value={name}
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
					</label>

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
						<datalist id="nss-key-groups">
							{#each suggestions as suggestion (suggestion)}
								<option value={suggestion}></option>
							{/each}
						</datalist>
						<span class="text-xs text-ink-muted">
							Members of this group can rotate, edit and revoke the key without an admin role.
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
							aria-invalid={expiresError}
							aria-describedby={expiresError ? 'nss-key-expires-error' : 'nss-key-expires-hint'}
							data-testid="nss-key-expires"
							class="{field} max-w-[420px]"
						/>
						{#if expiresError && maxLifetime}
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
							The expiry cannot be changed once the key exists.
						</span>
					</label>

					<label class="flex flex-col gap-1">
						<span class="text-sm font-medium text-ink">Notification email</span>
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
