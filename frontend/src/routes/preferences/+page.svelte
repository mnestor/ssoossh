<script lang="ts">
	import { SvelteSet } from 'svelte/reactivity';

	import { ApiError } from '#lib/api/client.js';
	import {
		getNotificationPreferences,
		listNSSShells,
		updateNotificationPreferences,
		updateShellPreference
	} from '#lib/api/endpoints.js';
	import type { DetailLevel, NotificationPreferences } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import Alert from '#lib/components/Alert.svelte';
	import DetailToggle from '#lib/components/DetailToggle.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SectionLabel from '#lib/components/SectionLabel.svelte';
	import Switch from '#lib/components/Switch.svelte';
	import { session } from '#lib/session.svelte.js';

	// Every setting on this page is live: it applies on the press and is
	// saved at once, with no Save button anywhere (form-ux: one save model
	// per screen, and a toggle applies at once). The Pages level also sits
	// in the heading of every covered page, where it has to be live, which
	// is what decided the model for the rest.

	// The list of notifications is served, not hardcoded here: adding a
	// notification kind or group is a server-side change (see server/notify),
	// and this page renders whatever it is given.
	let preferences = $state<NotificationPreferences | null>(null);
	let loadError = $state<string | null>(null);
	let hasLoaded = $state(false);
	// Bumped by Try again; the load below reads it, so a bump re-runs it.
	let attempt = $state(0);

	// Per kind: the value shown while its save is in flight, the kinds whose
	// save is in flight (a second press waits for the first to answer, so
	// two saves of one kind can never land out of order), and the kinds
	// whose last save failed.
	let shown = $state<Record<string, boolean>>({});
	const inFlight = new SvelteSet<string>();
	const failed = new SvelteSet<string>();

	// The email detail level, held the same way.
	let emailLevelChoice = $state<DetailLevel | null>(null);
	let emailLevelError = $state(false);
	const emailLevel = $derived(emailLevelChoice ?? preferences?.detail_level ?? 'simplified');

	// What a screen reader hears after a save lands. The switch's own state
	// change is the visible confirmation.
	let liveMessage = $state('');

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

	// The login shell, for a reader with a directory account that hosts
	// resolve (session.user.posix is null otherwise, and with NSS off). The
	// allowed names are the server's; the choice saves on change like every
	// other setting here, and a refused save puts the select back.
	const posix = $derived(session.user?.posix ?? null);
	let shells = $state<string[] | null>(null);
	let shellsError = $state<string | null>(null);
	let shellsAttempt = $state(0);
	// The value shown while a save is in flight; undefined when none is.
	let shellChoice = $state<string | null | undefined>(undefined);
	let shellSaving = $state(false);
	let shellError = $state<string | null>(null);
	const storedShell = $derived(posix?.shell_pref ?? null);
	const shownShell = $derived(shellChoice !== undefined ? shellChoice : storedShell);
	// A stored shell the server no longer offers stays visible as chosen
	// rather than silently reading as the directory default.
	const retiredShell = $derived(
		storedShell !== null && shells !== null && !shells.includes(storedShell) ? storedShell : null
	);

	$effect(() => {
		if (!posix) return;
		void shellsAttempt;
		const controller = new AbortController();
		listNSSShells(controller.signal)
			.then((result) => {
				shells = result.allowed_shells;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				shellsError = errorMessage(cause);
			});
		return () => controller.abort();
	});

	function retryShells() {
		shellsError = null;
		shells = null;
		shellsAttempt += 1;
	}

	/** chooseShell saves the login shell the moment it is chosen; '' is the directory default. */
	async function chooseShell(value: string) {
		const next = value === '' ? null : value;
		if (shellSaving || next === storedShell) {
			return;
		}
		shellChoice = next;
		shellSaving = true;
		shellError = null;
		try {
			const stored = await updateShellPreference(next);
			if (session.user) {
				session.user = { ...session.user, posix: stored };
			}
			liveMessage = next ? `Login shell ${next}. Saved.` : 'Login shell: directory default. Saved.';
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			// A 400 is the server refusing the name (shell_not_allowed), and its
			// message says which shells it allows; anything else is a retry.
			shellError =
				cause instanceof ApiError && cause.status === 400
					? `Not saved. ${cause.message}`
					: 'Not saved. Try again.';
		} finally {
			shellChoice = undefined;
			shellSaving = false;
		}
	}

	/** enabledFor is what a switch shows: its in-flight value, otherwise what the server said. */
	function enabledFor(kind: string, stored: boolean): boolean {
		return kind in shown ? shown[kind] : stored;
	}

	/**
	 * setKind saves one kind the moment it is pressed. Only that kind is
	 * sent, so a tab loaded before an upgrade cannot reset kinds it has never
	 * heard of. A failed save puts the switch back and says so beside it.
	 */
	async function setKind(kind: string, title: string, next: boolean) {
		if (inFlight.has(kind)) {
			return;
		}
		inFlight.add(kind);
		failed.delete(kind);
		shown = { ...shown, [kind]: next };
		try {
			const stored = await updateNotificationPreferences({ [kind]: next });
			// Kinds only: a level change in flight keeps its own state.
			if (preferences) {
				preferences = { ...preferences, kinds: stored.kinds };
			}
			liveMessage = `${title} ${next ? 'on' : 'off'}. Saved.`;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			failed.add(kind);
		} finally {
			const rest = { ...shown };
			delete rest[kind];
			shown = rest;
			inFlight.delete(kind);
		}
	}

	/** chooseEmailLevel saves the email detail level the moment it is pressed. */
	async function chooseEmailLevel(level: DetailLevel) {
		if (!preferences || level === emailLevel) {
			return;
		}
		emailLevelChoice = level;
		emailLevelError = false;
		try {
			// An empty kinds map changes no kind.
			const stored = await updateNotificationPreferences({}, level);
			if (preferences) {
				preferences = { ...preferences, detail_level: stored.detail_level };
			}
			liveMessage = `Email detail ${level}. Saved.`;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			emailLevelError = true;
		} finally {
			// Back to what the server holds, unless a newer press replaced
			// this one while it was in flight.
			if (emailLevelChoice === level) {
				emailLevelChoice = null;
			}
		}
	}
</script>

<svelte:head><title>Preferences · ssoossh</title></svelte:head>

<PageShell width="wide">
	<!-- "Preferences", not "Notification preferences": the page holds the
	     display level and the login shell as well. -->
	<PageHeading title="Preferences" />

	<div aria-live="polite" aria-atomic="true" class="sr-only" data-testid="preferences-status">
		{liveMessage}
	</div>

	{#if loadError}
		<Alert
			variant="error"
			title="Could not load your notification settings"
			onretry={retry}
			testid="preferences-error"
		>
			{loadError}
		</Alert>
	{:else if !hasLoaded}
		<!-- One row for the email detail and one per kind, so the page does
		     not jump when they arrive. -->
		<LoadingBlock
			shape="lines"
			count={8}
			label="Loading your notification settings…"
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
			description="Choose which events ssoossh emails you about and how much each email carries. These are about your own certificates and service codes. Changes save as you make them."
		>
			<!-- The card is the page's width; the rows are not. Each row is a
			     title and a sentence explaining when the notification fires,
			     and a sentence set 1100px wide is one the eye loses its place
			     in on the way back to the left. -->
			<div class="flex max-w-[70ch] flex-col gap-4">
				{#if preferences.address}
					<p class="text-dense text-ink-muted">
						Sent to <span class="text-ink">{preferences.address}</span>
					</p>
				{/if}

				<div class="flex flex-col gap-2" data-testid="email-detail">
					<span class="text-sm font-medium text-ink">Email detail</span>
					<div class="inline-flex flex-wrap items-center gap-x-3 gap-y-1">
						<DetailToggle
							testid="email-detail-toggle"
							label="Email detail"
							value={emailLevel}
							onselect={chooseEmailLevel}
						/>
						{#if emailLevelError}
							<!-- The selection has already snapped back; this says why. -->
							<span role="alert" class="text-xs text-danger" data-testid="email-detail-error">
								Not saved. Try again.
							</span>
						{/if}
					</div>
					<p class="text-dense text-ink-muted">
						Simplified sends what you need to recognise the event and act. Advanced adds key IDs,
						serials and certificate options.
					</p>
				</div>

				{#each preferences.groups as group (group.group)}
					{@const kinds = preferences.kinds.filter((kind) => kind.group === group.group)}
					{#if kinds.length > 0}
						<!-- A SectionLabel opens a sub-group inside a panel (DESIGN.md,
						     "Planes"); role=group names the set for a screen reader. -->
						<div
							role="group"
							aria-label={group.title}
							class="flex flex-col gap-3"
							data-testid="notification-group-{group.group}"
						>
							<div>
								<SectionLabel>{group.title}</SectionLabel>
								<p class="text-dense text-ink-muted">{group.description}</p>
							</div>
							{#each kinds as kind (kind.kind)}
								<div class="flex flex-col gap-1">
									<Switch
										checked={enabledFor(kind.kind, kind.enabled)}
										title={kind.title}
										description={kind.description}
										busy={inFlight.has(kind.kind)}
										onchange={(next) => setKind(kind.kind, kind.title, next)}
										testid={`notification-${kind.kind}`}
									/>
									{#if failed.has(kind.kind)}
										<!-- The switch has already snapped back; this says why. -->
										<span
											role="alert"
											class="pl-12 text-xs text-danger"
											data-testid={`notification-${kind.kind}-error`}
										>
											Not saved. Try again.
										</span>
									{/if}
								</div>
							{/each}
						</div>
					{/if}
				{/each}

				{#if preferences.kinds.length === 0}
					<p class="text-dense text-ink-muted">This server offers no email notifications.</p>
				{/if}
			</div>
		</PageSection>
	{/if}

	{#if posix}
		<!-- Outside the notification load, like Display: the account arrives
		     with the session. Hidden for a reader no host resolves. -->
		<PageSection
			title="Login shell"
			description="The shell hosts start when you sign in to them. Changes save as you make them."
			testid="login-shell"
		>
			<div class="flex max-w-[70ch] flex-col gap-3">
				<p class="text-dense text-ink-muted">
					Hosts know you as <span class="font-mono text-ink">{posix.name}</span>, uid
					<span class="text-ink tabular-nums">{posix.uid}</span>.
				</p>
				{#if shellsError}
					<Alert
						variant="error"
						title="Could not load the shells this server allows"
						onretry={retryShells}
						testid="login-shell-error">{shellsError}</Alert
					>
				{:else if shells === null}
					<LoadingBlock shape="lines" count={1} testid="login-shell-loading" />
				{:else}
					<label class="flex max-w-[420px] flex-col gap-1">
						<span class="text-sm font-medium text-ink">Shell</span>
						<select
							value={shownShell ?? ''}
							onchange={(event) => chooseShell(event.currentTarget.value)}
							disabled={shellSaving}
							aria-busy={shellSaving}
							aria-describedby="login-shell-help"
							data-testid="login-shell-select"
							class="min-h-target rounded-md border border-border-control bg-surface px-3 py-2 text-dense text-ink hover:bg-surface-muted disabled:opacity-50"
						>
							<option value="">Directory default</option>
							{#each shells as shell (shell)}
								<option value={shell}>{shell}</option>
							{/each}
							{#if retiredShell}
								<option value={retiredShell}>{retiredShell} (no longer offered)</option>
							{/if}
						</select>
					</label>
					{#if shellError}
						<!-- The select has already snapped back; this says why. -->
						<span role="alert" class="text-xs text-danger" data-testid="login-shell-save-error">
							{shellError}
						</span>
					{/if}
					<p id="login-shell-help" class="text-dense text-ink-muted">
						Hosts use this shell if installed; otherwise the directory shell.
					</p>
				{/if}
			</div>
		</PageSection>
	{/if}

	<!-- Outside the notification load: the pages' level arrives with the
	     session, so a failed read of the notification settings must not take
	     it with it. Live like everything else on the page. -->
	<PageSection
		title="Display"
		description="How much the approval, certificate and service code pages show. Changes save as you make them, and apply in every browser you sign in to."
		testid="display-preferences"
	>
		<div class="flex max-w-[70ch] flex-col gap-3">
			<DetailToggle testid="display-detail" label="Page detail" />
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
