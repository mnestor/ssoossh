<script lang="ts">
	import { getNotificationPreferences, updateNotificationPreferences } from '#lib/api/endpoints.js';
	import type { DetailLevel, NotificationPreferences } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import Button from '#lib/components/Button.svelte';
	import DetailToggle from '#lib/components/DetailToggle.svelte';
	import MonoChip from '#lib/components/MonoChip.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// The list of notifications is served, not hardcoded here: adding a
	// notification kind is a server-side change (see server/notify), and this
	// page renders whatever it is given so a new one needs no frontend edit.
	let preferences = $state<NotificationPreferences | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);
	// Bumped by Try again; the load below reads it, so a bump re-runs it.
	let attempt = $state(0);

	// pending holds only the toggles the user has actually moved. Sending
	// just those is what stops a tab loaded before an upgrade from resetting
	// kinds it has never heard of.
	let pending = $state<Record<string, boolean>>({});
	// The email detail level, held like a toggle: an edit until saved, and
	// dropped again when put back to what the server said.
	let pendingLevel = $state<DetailLevel | null>(null);
	let saving = $state(false);
	let saveError = $state<string | null>(null);
	let saved = $state(false);

	const hasChanges = $derived(Object.keys(pending).length > 0 || pendingLevel !== null);
	const emailLevel = $derived(pendingLevel ?? preferences?.detail_level ?? 'simplified');

	/** chooseEmailLevel records an edit to the email detail level. */
	function chooseEmailLevel(level: DetailLevel) {
		saved = false;
		pendingLevel = level === preferences?.detail_level ? null : level;
	}

	// The way out of a failed load: back to the loading shape, then the
	// same request again.
	function retry() {
		loadError = null;
		hasLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
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
			preferences = await updateNotificationPreferences(pending, pendingLevel ?? undefined);
			pending = {};
			pendingLevel = null;
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

<PageShell width="wide">
	<!-- "Preferences", not "Notification preferences": the page now holds
	     the display level as well, and will hold the login shell. -->
	<PageHeading title="Preferences" />

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load your preferences"
			onretry={retry}
			testid="preferences-error"
		>
			{loadError}
		</Alert>
	{:else if !hasLoaded}
		<LoadingBlock
			shape="lines"
			count={4}
			label="Loading your preferences…"
			testid="preferences-loading"
		/>
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

		<PageSection
			title="Email notifications"
			description="Choose which events ssoossh emails you about. These are notifications about your own certificates and enrollments."
		>
			<!-- The card is the page's width; the toggles are not. Each row is
			     a title and a sentence explaining when the notification
			     fires, and a sentence set 1100px wide is one the eye loses
			     its place in on the way back to the left. -->
			<div class="flex max-w-[70ch] flex-col gap-4">
				{#if preferences.address}
					<p class="text-dense text-ink-muted">
						Sent to <MonoChip>{preferences.address}</MonoChip>
					</p>
				{/if}

				<!-- Separate from Display below: short mail and a full approval
				     page are a common pairing. Saved with the toggles. -->
				<div class="flex flex-col gap-2" data-testid="email-detail">
					<span class="text-sm font-medium text-ink">Email detail</span>
					<DetailToggle
						testid="email-detail-toggle"
						label="Email detail level"
						value={emailLevel}
						onselect={chooseEmailLevel}
					/>
					<p class="text-dense text-ink-muted">
						Simplified sends what you need to recognise the event and act. Advanced adds key IDs,
						serials and certificate options.
					</p>
				</div>

				{#each preferences.kinds as kind (kind.kind)}
					<label class="flex min-h-target cursor-pointer items-start gap-3">
						<input
							type="checkbox"
							checked={enabledFor(kind.kind, kind.enabled)}
							onchange={() => toggle(kind.kind, kind.enabled)}
							data-testid={`notification-${kind.kind}`}
							class="mt-0.5 size-4 shrink-0"
						/>
						<span class="flex flex-col gap-0.5">
							<span class="text-sm font-medium text-ink">{kind.title}</span>
							<span class="text-dense text-ink-muted">{kind.description}</span>
						</span>
					</label>
				{/each}

				{#if preferences.kinds.length === 0}
					<p class="text-dense text-ink-muted">This server offers no email notifications.</p>
				{/if}
			</div>
		</PageSection>

		{#if saveError}
			<Alert variant="error" title="Could not save your preferences">{saveError}</Alert>
		{:else if saved}
			<Alert variant="info" title="Preferences saved">
				Your notification choices and email detail are up to date.
			</Alert>
		{/if}

		<div class="flex justify-end">
			<Button onclick={save} disabled={!hasChanges} busy={saving} testid="save-preferences">
				{saving ? 'Saving…' : 'Save notifications'}
			</Button>
		</div>
	{/if}

	<!-- Outside the notification load: it arrives with the session rather
	     than with the notification settings, so a failed read of those must
	     not take it with it. It applies and saves at once, unlike the
	     notifications above, and says so, since the Save button belongs to
	     those alone. -->
	<PageSection
		title="Display"
		description="How much the approval, certificate and service code pages show. Takes effect at once and is saved to your account."
		testid="display-preferences"
	>
		<div class="flex max-w-[70ch] flex-col gap-3">
			<DetailToggle testid="display-detail" />
			<dl class="flex flex-col gap-1 text-dense">
				<div>
					<dt class="inline font-medium text-ink">Simplified</dt>
					<dd class="inline text-ink-muted">
						keeps what you need to decide or to understand the result.
					</dd>
				</div>
				<div>
					<dt class="inline font-medium text-ink">Advanced</dt>
					<dd class="inline text-ink-muted">
						adds the ids, fingerprints and policy detail an expert compares.
					</dd>
				</div>
			</dl>
		</div>
	</PageSection>
</PageShell>
