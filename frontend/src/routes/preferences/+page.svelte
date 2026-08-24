<script lang="ts">
	import { getNotificationPreferences, updateNotificationPreferences } from '$lib/api/endpoints';
	import type { NotificationPreferences } from '$lib/api/types';
	import { errorMessage, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';

	// The list of notifications is served, not hardcoded here: adding a
	// notification kind is a server-side change (see server/notify), and this
	// page renders whatever it is given so a new one needs no frontend edit.
	let preferences = $state<NotificationPreferences | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);

	// pending holds only the toggles the user has actually moved. Sending
	// just those is what stops a tab loaded before an upgrade from resetting
	// kinds it has never heard of.
	let pending = $state<Record<string, boolean>>({});
	let saving = $state(false);
	let saveError = $state<string | null>(null);
	let saved = $state(false);

	const hasChanges = $derived(Object.keys(pending).length > 0);

	$effect(() => {
		const controller = new AbortController();

		getNotificationPreferences(controller.signal)
			.then((result) => {
				preferences = result;
				hasLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) {
					return;
				}
				loadError = errorMessage(cause);
				hasLoaded = true;
			});

		return () => controller.abort();
	});

	/** enabledFor is the value a toggle should show: the pending edit if there is one, otherwise what the server said. */
	function enabledFor(kind: string, stored: boolean): boolean {
		return kind in pending ? pending[kind] : stored;
	}

	/**
	 * toggle records an edit, or drops it again when the user puts a toggle
	 * back where it started — so returning to the loaded state leaves nothing
	 * to save rather than saving a no-op.
	 */
	function toggle(kind: string, stored: boolean) {
		saved = false;
		const next = !enabledFor(kind, stored);
		if (next === stored) {
			const rest = { ...pending };
			delete rest[kind];
			pending = rest;
			return;
		}
		pending = { ...pending, [kind]: next };
	}

	async function save() {
		if (!hasChanges) {
			return;
		}

		saving = true;
		saveError = null;
		saved = false;
		try {
			// The server answers with the preferences as stored, so the page
			// shows what was actually saved rather than what it submitted.
			preferences = await updateNotificationPreferences(pending);
			pending = {};
			saved = true;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			// The edit is deliberately kept: losing someone's unsaved change
			// to report that saving failed makes the failure worse.
			saveError = errorMessage(cause);
		} finally {
			saving = false;
		}
	}
</script>

<svelte:head><title>Preferences · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[680px] flex-col gap-5">
	<PageHeading eyebrow="Preferences" title="Notifications" />

	{#if loadError}
		<Alert variant="error" title="Could not load your preferences">{loadError}</Alert>
	{:else if !hasLoaded}
		<p class="text-sm text-ink-muted">Loading…</p>
	{:else if preferences}
		{#if !preferences.mail_enabled}
			<Alert variant="warning" title="Email is not configured">
				This server is not configured to send email, so nothing is delivered. Your choices below are
				still saved and take effect once an administrator configures a mail relay.
			</Alert>
		{:else if !preferences.address}
			<Alert variant="warning" title="Nowhere to send">
				Your identity carries no email address, so nothing can be delivered. Your choices below are
				still saved.
			</Alert>
		{/if}

		<Card
			title="Email notifications"
			description="Choose which events ssoossh emails you about. These are notifications about your own certificates and enrollments."
		>
			<div class="flex flex-col gap-4">
				{#if preferences.address}
					<p class="text-[13px] text-ink-muted">
						Sent to <MonoChip>{preferences.address}</MonoChip>
					</p>
				{/if}

				{#each preferences.kinds as kind (kind.kind)}
					<label class="flex cursor-pointer items-start gap-3">
						<input
							type="checkbox"
							checked={enabledFor(kind.kind, kind.enabled)}
							onchange={() => toggle(kind.kind, kind.enabled)}
							data-testid={`notification-${kind.kind}`}
							class="mt-0.5 h-4 w-4 shrink-0 rounded border-border-subtle text-accent"
						/>
						<span class="flex flex-col gap-0.5">
							<span class="text-sm font-medium text-ink">{kind.title}</span>
							<span class="text-[13px] text-ink-muted">{kind.description}</span>
						</span>
					</label>
				{/each}

				{#if preferences.kinds.length === 0}
					<p class="text-[13px] text-ink-muted">This server offers no email notifications.</p>
				{/if}
			</div>
		</Card>

		{#if saveError}
			<Alert variant="error" title="Could not save your preferences">{saveError}</Alert>
		{:else if saved}
			<Alert variant="info" title="Preferences saved">
				Your notification choices are up to date.
			</Alert>
		{/if}

		<div class="flex justify-end">
			<Button onclick={save} disabled={!hasChanges} busy={saving} testid="save-preferences">
				{saving ? 'Saving…' : 'Save'}
			</Button>
		</div>
	{/if}
</div>
