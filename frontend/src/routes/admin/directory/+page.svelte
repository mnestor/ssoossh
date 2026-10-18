<script lang="ts">
	import { tick } from 'svelte';
	import { resolve } from '$app/paths';

	import { getLDAPStatus, runLDAPSync, probeLDAP } from '#lib/api/endpoints.js';
	import type {
		LDAPProbeRequestBody,
		LDAPProbeResponse,
		LDAPStatusResponse,
		LDAPSyncRunResponse
	} from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { formatDateTime } from '#lib/format.js';
	import Alert from '#lib/components/Alert.svelte';
	import LoadingBlock from '#lib/components/LoadingBlock.svelte';
	import Button from '#lib/components/Button.svelte';
	import ConfirmModal from '#lib/components/ConfirmModal.svelte';
	import Icon from '#lib/components/Icon.svelte';
	import MonoChip from '#lib/components/MonoChip.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SectionLabel from '#lib/components/SectionLabel.svelte';
	import NSSDirectoryPanels from '#lib/components/NSSDirectoryPanels.svelte';
	import NSSStoredEntries from '#lib/components/NSSStoredEntries.svelte';
	import { session } from '#lib/session.svelte.js';

	let status = $state<LDAPStatusResponse | null>(null);
	// The NSS panels hang off the directory, and only exist where the server
	// serves NSS; nss.enabled requires ldap.enabled, so they live inside the
	// enabled branch below.
	const nssEnabled = $derived(session.user?.nss_enabled ?? false);
	let nssMessage = $state('');
	let statusError = $state<string | null>(null);
	let statusLoaded = $state(false);
	// Bumped by Try again; the status load reads it, so a bump re-runs it.
	let attempt = $state(0);

	// Sync state. The run record is the answer whatever happened, including
	// a pass that could not reach the directory, so it is rendered rather
	// than collapsed into a success or failure banner.
	let syncing = $state(false);
	let lastRun = $state<LDAPSyncRunResponse | null>(null);
	let syncError = $state<string | null>(null);
	let dryRun = $state(true);
	// A live pass can disable accounts, so it confirms first (DESIGN.md,
	// "Destructive actions"); a dry run changes nothing and runs at once.
	let showSyncConfirm = $state(false);

	// Probe form. There is deliberately no connection here: the probe uses
	// the running url, bind credentials and base DN, and cannot be
	// re-pointed. What an operator varies is the question.
	let mode = $state<'template' | 'literal'>('template');
	let filter = $state('');
	let attributes = $state('');
	let bindingSource = $state<'self' | 'custom'>('self');
	let bindUsername = $state('');
	let bindEmail = $state('');
	let bindSubject = $state('');

	let probing = $state(false);
	let probeResult = $state<LDAPProbeResponse | null>(null);
	let probeError = $state<string | null>(null);
	let view = $state<'entry' | 'fields' | 'merge'>('entry');

	// The probe result's three views, in the order a reader works through
	// them: what came back, what each field made of it, what was kept.
	const views = [
		{ id: 'entry', label: 'Entry as returned' },
		{ id: 'fields', label: 'Field mapping' },
		{ id: 'merge', label: 'Merge and allowlist' }
	] as const;

	// The way out of a failed status load: back to the loading shape, then
	// the same request again.
	function retryStatus() {
		statusError = null;
		statusLoaded = false;
		attempt += 1;
	}

	$effect(() => {
		void attempt;
		const controller = new AbortController();
		getLDAPStatus(controller.signal)
			.then((result) => {
				status = result;
				lastRun = result.last_run ?? null;
				statusLoaded = true;
			})
			.catch((cause) => {
				if (controller.signal.aborted || redirectIfUnauthenticated(cause)) return;
				statusError = errorMessage(cause);
				statusLoaded = true;
			});
		return () => controller.abort();
	});

	// The NSS directory viewer links here to a section by its fragment
	// (#probe, #collisions). Every section waits on the status read, so the
	// router's own scroll on arrival finds nothing; once the sections are
	// drawn, bring the named one into view. Once only: a later re-read must
	// not pull the reader back.
	let scrolledToFragment = false;
	$effect(() => {
		if (!statusLoaded || scrolledToFragment) return;
		scrolledToFragment = true;
		const fragment = window.location.hash.slice(1);
		if (!fragment) return;
		void tick().then(() => document.getElementById(fragment)?.scrollIntoView());
	});

	// Set when the status could not be read back after a pass: the page then
	// shows the pass's own counts over a status that may be out of date,
	// and says so instead of failing silently.
	let statusStale = $state(false);

	async function refreshStatus() {
		try {
			status = await getLDAPStatus();
			statusStale = false;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			statusStale = true;
			// The sync result is already on screen; a failed status refresh
			// is not worth replacing it with an error.
		}
	}

	/** runSync runs one pass and reports whether it ran. */
	async function runSync(): Promise<boolean> {
		syncing = true;
		syncError = null;
		try {
			lastRun = await runLDAPSync({ dry_run: dryRun });
			await refreshStatus();
			return true;
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return false;
			syncError = errorMessage(cause);
			return false;
		} finally {
			syncing = false;
		}
	}

	function handleSync() {
		if (dryRun) {
			void runSync();
			return;
		}
		syncError = null;
		showSyncConfirm = true;
	}

	// A failure stays in the dialog, beside the button that caused it.
	async function confirmLiveSync() {
		if (await runSync()) {
			showSyncConfirm = false;
		}
	}

	function cancelLiveSync() {
		showSyncConfirm = false;
		syncError = null;
	}

	async function handleProbe() {
		probing = true;
		probeError = null;
		probeResult = null;
		try {
			const body: LDAPProbeRequestBody = {
				mode,
				filter: filter.trim() || undefined,
				attributes: parseAttributes(attributes),
				binding_source: bindingSource
			};
			if (bindingSource === 'custom') {
				body.bindings = {
					username: bindUsername || undefined,
					email: bindEmail || undefined,
					subject: bindSubject || undefined
				};
			}
			probeResult = await probeLDAP(body);
			view = 'entry';
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) return;
			probeError = errorMessage(cause);
		} finally {
			probing = false;
		}
	}

	/** parseAttributes splits the comma or space separated attribute list.
	 * Empty means every user attribute, which is the point of the console —
	 * you cannot map the field you should have mapped if you can only see
	 * the ones the config already names. */
	function parseAttributes(raw: string): string[] | undefined {
		const names = raw
			.split(/[\s,]+/)
			.map((name) => name.trim())
			.filter((name) => name.length > 0);
		return names.length > 0 ? names : undefined;
	}

	/** formatDuration renders a seconds count the way the config file spells
	 * it, so what the page says and what an operator would write agree. */
	function formatDuration(seconds: number): string {
		if (seconds <= 0) return 'off';
		if (seconds % 3600 === 0) return `${seconds / 3600}h`;
		if (seconds % 60 === 0) return `${seconds / 60}m`;
		return `${seconds}s`;
	}

	function formatTime(value?: string | null): string {
		return value ? formatDateTime(value) : '—';
	}

	// The counts that need a second look: a failure on any pass, and a
	// disable on a live one. Every chip carries a number, so the reader
	// used to have to find the non-zero ones themselves.
	function countTone(
		kind: 'failed' | 'disabled',
		run: { dry_run?: boolean; failed: number; disabled: number }
	): string {
		const value = kind === 'failed' ? run.failed : run.disabled;
		const flagged = value > 0 && (kind === 'failed' || !run.dry_run);
		return flagged ? 'bg-trimmed-surface text-trimmed' : '';
	}
</script>

<svelte:head><title>Directory · ssoossh</title></svelte:head>

<!-- One option of a segmented control. Selected is an ink fill with the
     check glyph and aria-pressed, never cobalt (DESIGN.md, "Selected,
     current and status"): these rows used to mark their choice with the
     accent hue alone. -->
{#snippet toggle(label: string, pressed: boolean, testid: string, onclick: () => void)}
	<button
		type="button"
		class="inline-flex min-h-target items-center gap-1.5 rounded-md border px-3 text-xs font-medium transition {pressed
			? 'border-ink bg-ink text-surface'
			: 'border-border-control text-ink-muted hover:bg-surface-muted'}"
		aria-pressed={pressed}
		data-testid={testid}
		{onclick}
	>
		{#if pressed}<Icon name="check" size="xs" />{/if}
		{label}
	</button>
{/snippet}

<PageShell width="wide">
	<PageHeading title="Directory">
		{#snippet sub()}
			What the LDAP sync last did, and what the directory actually returns for a person.
			{#if nssEnabled}
				The fleet keys hosts use for NSS lookups are on the
				<a
					href={resolve('/nss-keys')}
					class="text-accent hover:underline"
					data-testid="directory-nss-keys-link">NSS keys</a
				> page.
			{/if}
		{/snippet}
	</PageHeading>

	<div aria-live="polite" aria-atomic="true" class="sr-only" data-testid="directory-nss-status">
		{nssMessage}
	</div>

	{#if statusError}
		<Alert
			variant="error"
			title="Could not load the directory status"
			onretry={retryStatus}
			testid="directory-error"
		>
			{statusError}
		</Alert>
	{:else if !statusLoaded}
		<LoadingBlock
			shape="lines"
			count={3}
			label="Loading the directory status…"
			testid="directory-loading"
		/>
	{:else if status && !status.enabled}
		<Alert variant="info" title="Directory enrichment is off" testid="ldap-disabled">
			<code>ldap.enabled</code> is false, so nothing is synced and there is nothing to probe. OIDC claims
			alone drive every operation.
		</Alert>
	{:else if status}
		<!-- Status and sync. The run record is what makes a sync reportable:
		     before it, a pass that ran and one that never fired looked
		     identical from outside the log. -->
		<PageSection
			title="Sync"
			description="The scheduled pass, and the last one that ran on any instance."
			testid="ldap-sync-card"
			id="sync"
		>
			<div class="flex flex-col gap-5">
				<dl class="grid gap-4 sm:grid-cols-3">
					<div>
						<dt class="text-xs font-semibold text-ink-muted">Interval</dt>
						<dd class="font-mono text-sm">{formatDuration(status.sync_interval_seconds)}</dd>
					</div>
					<div>
						<dt class="text-xs font-semibold text-ink-muted">Disable after missing for</dt>
						<dd class="font-mono text-sm">{formatDuration(status.disable_after_seconds)}</dd>
					</div>
					<div>
						<dt class="text-xs font-semibold text-ink-muted">Re-enable on return</dt>
						<dd class="font-mono text-sm">{status.reenable ? 'yes' : 'no'}</dd>
					</div>
				</dl>

				{#if status.sync_interval_seconds === 0}
					<Alert variant="warning" title="The scheduled sync is off" testid="ldap-sync-off">
						<code>ldap.sync.interval</code> is zero, so nothing runs on its own. Directory data refreshes
						at login, and only for the person logging in.
					</Alert>
				{/if}

				{#if lastRun}
					<div data-testid="ldap-last-run">
						<div class="mb-3 flex flex-wrap items-baseline gap-x-3 gap-y-1">
							<SectionLabel>Last pass</SectionLabel>
							<span class="text-xs text-ink-muted">
								{lastRun.trigger === 'manual' ? 'run by hand' : 'scheduled'}
								{#if lastRun.actor_username}by {lastRun.actor_username}{/if}
								{#if lastRun.dry_run}· dry run{/if}
								{#if lastRun.instance}· on {lastRun.instance}{/if}
							</span>
						</div>

						<dl class="grid gap-3 text-sm sm:grid-cols-3">
							<div>
								<dt class="text-xs text-ink-muted">Started</dt>
								<dd>{formatTime(lastRun.started_at)}</dd>
							</div>
							<div>
								<dt class="text-xs text-ink-muted">Finished</dt>
								<dd>{lastRun.finished_at ? formatTime(lastRun.finished_at) : 'still running'}</dd>
							</div>
							<div>
								<dt class="text-xs text-ink-muted">Users</dt>
								<dd class="font-mono">{lastRun.users_seen}</dd>
							</div>
						</dl>

						<div class="mt-3 flex flex-wrap gap-1.5">
							<MonoChip>{lastRun.found} found</MonoChip>
							<MonoChip>{lastRun.missing} missing</MonoChip>
							<span
								class="rounded border border-border-subtle px-1.5 py-0.5 font-mono text-xs text-ink {countTone(
									'failed',
									lastRun
								)}"
								data-testid="ldap-count-failed">{lastRun.failed} failed</span
							>
							<span
								class="rounded border border-border-subtle px-1.5 py-0.5 font-mono text-xs text-ink {countTone(
									'disabled',
									lastRun
								)}"
								data-testid="ldap-count-disabled"
								>{lastRun.disabled} {lastRun.dry_run ? 'would disable' : 'disabled'}</span
							>
							<MonoChip
								>{lastRun.reenabled} {lastRun.dry_run ? 'would re-enable' : 're-enabled'}</MonoChip
							>
						</div>

						{#if lastRun.error}
							<div class="mt-3">
								<Alert variant="error" title="The pass could not complete" testid="ldap-run-error">
									{lastRun.error}
								</Alert>
							</div>
						{/if}
					</div>
				{:else}
					<p class="text-sm text-ink-muted" data-testid="ldap-no-runs">
						No sync pass has been recorded yet, on any instance.
					</p>
				{/if}

				{#if statusStale}
					<p class="text-dense text-trimmed" data-testid="ldap-status-stale">
						The status above may be out of date: it could not be read again after the pass.
						<button
							type="button"
							class="inline-flex min-h-target items-center font-medium text-accent hover:underline"
							onclick={refreshStatus}>Try again</button
						>
					</p>
				{/if}

				<div class="flex flex-col gap-3 border-t border-border-subtle pt-4">
					<label class="flex min-h-target items-start gap-2 text-sm">
						<input type="checkbox" bind:checked={dryRun} data-testid="ldap-dry-run" class="mt-1" />
						<span>
							<span class="font-medium">Dry run</span>
							<span class="block text-dense text-ink-muted">
								Read the directory and report what the pass would do, changing nothing. Safe to
								press during an incident.
							</span>
						</span>
					</label>

					<div>
						<Button testid="ldap-run-sync" busy={syncing} onclick={handleSync}>
							{syncing ? 'Running…' : dryRun ? 'Dry run now' : 'Sync now'}
						</Button>
					</div>

					{#if !dryRun}
						<p class="text-dense text-trimmed" data-testid="ldap-live-warning">
							A live pass can disable an account whose directory entry has been missing longer than {formatDuration(
								status.disable_after_seconds
							)}. It runs on this instance and behaves exactly like the scheduled one.
						</p>
					{/if}

					{#if syncError && !showSyncConfirm}
						<Alert variant="error" title="The sync did not run" testid="ldap-sync-error">
							{syncError}
						</Alert>
					{/if}

					{#if showSyncConfirm}
						<ConfirmModal
							title="Run a live sync now?"
							reasonless
							confirmLabel="Sync now"
							busyLabel="Running…"
							busy={syncing}
							error={syncError}
							errorTitle="The sync did not run"
							confirmTestid="confirm-live-sync"
							onconfirm={confirmLiveSync}
							oncancel={cancelLiveSync}
						>
							A live pass can disable an account whose directory entry has been missing longer than {formatDuration(
								status.disable_after_seconds
							)}. It runs on this instance and behaves exactly like the scheduled one.
						</ConfirmModal>
					{/if}
				</div>
			</div>
		</PageSection>

		<!-- Probe console. Read-only by construction: it runs the login
			     path's lookup and field resolution and stops before the
			     write. -->
		<PageSection
			title="Probe"
			description="One read-only lookup against the configured directory. Nothing is written."
			testid="ldap-probe-card"
			id="probe"
		>
			<div class="flex flex-col gap-5">
				<dl class="grid gap-4 sm:grid-cols-2">
					<div>
						<dt class="text-xs font-semibold text-ink-muted">Server</dt>
						<dd class="font-mono text-sm break-all">{status.url}</dd>
					</div>
					<div>
						<dt class="text-xs font-semibold text-ink-muted">Base DN</dt>
						<dd class="font-mono text-sm break-all">{status.base_dn}</dd>
					</div>
				</dl>
				<p class="-mt-2 text-dense text-ink-muted">
					The connection is not part of the probe. It always uses the running server, bind
					credentials and base DN, so it cannot be pointed anywhere else.
				</p>

				{#if status.tls_insecure_skip_verify}
					<Alert
						variant="warning"
						title="Certificate verification is off"
						testid="ldap-insecure-tls"
					>
						<code>ldap.tls_insecure_skip_verify</code> is enabled, so a probe that succeeds proves the
						directory answered, not that it is the directory you meant.
					</Alert>
				{/if}

				<div class="grid gap-4 sm:grid-cols-2">
					<div>
						<SectionLabel for="ldap-filter">Filter</SectionLabel>
						<!-- Not a decorative pair of buttons: Template escapes the
							     value per RFC 4515 and Literal sends it verbatim, so
							     which one is pressed decides what reaches the
							     directory. It used to be said in the accent hue and
							     nothing else — invisible to a screen reader, and to
							     anyone who cannot separate teal from grey. -->
						<div class="mb-2 flex gap-1" role="group" aria-label="Filter mode">
							{@render toggle(
								'Template',
								mode === 'template',
								'ldap-mode-template',
								() => (mode = 'template')
							)}
							{@render toggle(
								'Literal',
								mode === 'literal',
								'ldap-mode-literal',
								() => (mode = 'literal')
							)}
						</div>
						<input
							id="ldap-filter"
							type="text"
							bind:value={filter}
							data-testid="ldap-filter"
							aria-describedby="ldap-filter-help"
							placeholder={mode === 'template'
								? status.user_filter
								: '(&(objectClass=person)(uid=mnestor))'}
							class="w-full rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-sm"
						/>
						<p id="ldap-filter-help" class="mt-1 text-dense text-ink-muted">
							{#if mode === 'template'}
								Rendered against the bindings below, with RFC 4515 escaping applied — which is the
								only way to see what your configured filter really sends. Empty uses
								<code>ldap.user_filter</code>.
							{:else}
								Sent exactly as typed. Nothing is interpolated, so nothing is escaped.
							{/if}
						</p>
					</div>

					<div>
						<SectionLabel for="ldap-attributes">Attributes</SectionLabel>
						<input
							id="ldap-attributes"
							type="text"
							bind:value={attributes}
							data-testid="ldap-attributes"
							aria-describedby="ldap-attributes-help"
							placeholder="* (every user attribute)"
							class="w-full rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-sm"
						/>
						<p id="ldap-attributes-help" class="mt-1 text-dense text-ink-muted">
							Empty asks for everything. Attributes the configuration reads are marked "mapped" in
							the result.
						</p>
					</div>
				</div>

				{#if mode === 'template'}
					<div>
						<SectionLabel>Bindings</SectionLabel>
						<div class="mb-2 flex gap-1" role="group" aria-label="Binding source">
							{@render toggle(
								'My session',
								bindingSource === 'self',
								'ldap-binding-self',
								() => (bindingSource = 'self')
							)}
							{@render toggle(
								'Typed values',
								bindingSource === 'custom',
								'ldap-binding-custom',
								() => (bindingSource = 'custom')
							)}
						</div>
						{#if bindingSource === 'custom'}
							<!-- Three fields that differ only in what they hold, so
								     the placeholder was the whole of each one's name —
								     and a placeholder is gone the moment anything is
								     typed. Named properly, because guessing which of
								     three identical boxes is the subject is not a game
								     to play during an LDAP incident. -->
							<div class="grid gap-2 sm:grid-cols-3" data-testid="ldap-binding-fields">
								<label class="flex flex-col gap-1">
									<span class="text-dense text-ink-muted">Username</span>
									<input
										type="text"
										bind:value={bindUsername}
										placeholder="alice"
										data-testid="ldap-bind-username"
										class="rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-sm"
									/>
								</label>
								<label class="flex flex-col gap-1">
									<span class="text-dense text-ink-muted">Email</span>
									<input
										type="text"
										bind:value={bindEmail}
										placeholder="alice@example.com"
										data-testid="ldap-bind-email"
										class="rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-sm"
									/>
								</label>
								<label class="flex flex-col gap-1">
									<span class="text-dense text-ink-muted">Subject</span>
									<input
										type="text"
										bind:value={bindSubject}
										placeholder="0f8c…"
										data-testid="ldap-bind-subject"
										class="rounded-md border border-border-control bg-surface px-3 py-2 font-mono text-sm"
									/>
								</label>
							</div>
							<p class="mt-1 text-dense text-ink-muted">
								Typed values are what let you test someone's entry before they have ever logged in.
							</p>
						{/if}
					</div>
				{/if}

				<div>
					<Button testid="ldap-run-probe" busy={probing} onclick={handleProbe}>
						{probing ? 'Probing…' : 'Run probe'}
					</Button>
				</div>

				{#if probeError}
					<Alert variant="error" title="The probe could not run" testid="ldap-probe-error">
						{probeError}
					</Alert>
				{/if}

				{#if probeResult}
					<div class="rounded-lg bg-surface-muted" data-testid="ldap-probe-result">
						<div class="border-b border-border-subtle px-4 py-3">
							<div class="flex flex-wrap items-baseline justify-between gap-2">
								<code class="text-sm break-all">{probeResult.filter_sent}</code>
								<span class="text-xs font-semibold tracking-wide text-ink-muted uppercase">
									Never writes
								</span>
							</div>
							<p class="mt-1 text-dense text-ink-muted">
								{probeResult.matched} entr{probeResult.matched === 1 ? 'y' : 'ies'} matched in
								{probeResult.elapsed_ms} ms of a {Math.round(probeResult.timeout_ms / 1000)}s
								timeout.
								{#if probeResult.matched > 1}
									The login path refuses anything but exactly one, so this filter is too loose.
								{/if}
							</p>
							<!-- The re-anchoring identifier, reported next to the filter
								     because it is the thing that decides whether this entry
								     can still be found after the filter stops matching it. -->
							{#if probeResult.matched > 0}
								<p class="mt-1 text-dense text-ink-muted" data-testid="ldap-probe-identifier">
									{#if probeResult.id_attribute}
										Anchored on <code>{probeResult.id_attribute}</code> =
										<code class="break-all"
											>{probeResult.directory_id || 'absent on this entry'}</code
										>
									{:else}
										<code>ldap.id_attribute</code> is unset, so this entry is re-read by DN and then
										by <code>ldap.user_filter</code>. Both move when someone is renamed or moved
										between OUs, which reads as a deletion and counts toward the auto-disable.
									{/if}
								</p>
							{/if}
						</div>

						{#if probeResult.matched === 0}
							<p class="px-4 py-6 text-sm text-ink-muted" data-testid="ldap-probe-no-match">
								The search succeeded and found nothing. That is an answer about the directory, not a
								failure: this is exactly what a login would see.
							</p>
						{:else}
							<!-- A group of toggles, not tabs (DESIGN.md, "Accessibility"):
								     the underline that marked the open view had no
								     aria-selected behind it, so a screen reader could not
								     tell which one was showing. -->
							<div
								class="flex flex-wrap gap-1 border-b border-border-subtle px-4 py-3"
								role="group"
								aria-label="Probe result view"
							>
								{#each views as v (v.id)}
									{@render toggle(v.label, view === v.id, `ldap-tab-${v.id}`, () => (view = v.id))}
								{/each}
							</div>

							{#if view === 'entry' && probeResult.entry}
								<div data-testid="ldap-panel-entry">
									<div
										class="border-b border-border-subtle px-4 py-2 font-mono text-dense break-all"
									>
										{probeResult.entry.dn}
									</div>
									{#each probeResult.entry.attributes as attr (attr.name)}
										<div
											class="grid grid-cols-[minmax(9rem,14rem)_1fr] gap-4 border-b border-border-subtle px-4 py-2 text-dense last:border-0"
										>
											<!-- Said in words, not a green row: colour alone
												     reached nobody who cannot see it, and green
												     means "granted", which this is not. -->
											<span class="flex flex-wrap items-baseline gap-x-2">
												<span class="font-mono break-all">{attr.name}</span>
												{#if attr.configured}
													<span class="text-xs text-ink-muted" data-testid="ldap-attr-mapped"
														>mapped</span
													>
												{/if}
											</span>
											<span class="flex flex-col gap-0.5 font-mono break-all">
												{#each attr.values as value, i (i)}
													<span>{value}</span>
												{/each}
												{#if attr.truncated_values}
													<span class="text-ink-muted"
														>…{attr.truncated_values} more value(s) not shown</span
													>
												{/if}
											</span>
										</div>
									{/each}
								</div>
							{:else if view === 'fields'}
								<div data-testid="ldap-panel-fields">
									{#each probeResult.fields ?? [] as field (field.name)}
										<div class="border-b border-border-subtle px-4 py-3 text-dense last:border-0">
											<p class="font-mono font-semibold">{field.name}</p>
											{#if field.attribute}
												<p class="text-ink-muted">
													attribute <code>{field.attribute}</code> →
													{field.attribute_values?.length ?? 0} value(s)
													{#if !field.attribute_present}
														<span class="text-trimmed">
															· not present on the entry, which usually means a typo</span
														>
													{/if}
												</p>
											{/if}
											{#each field.searches ?? [] as search (search.name)}
												<p class="text-ink-muted">
													search <code>{search.name}</code> → {search.entries} entr{search.entries ===
													1
														? 'y'
														: 'ies'}
													<span class="block font-mono break-all">{search.filter_sent}</span>
													{#if search.error}
														<span class="block text-danger">{search.error}</span>
													{/if}
												</p>
											{/each}
											<p class="mt-1 font-mono break-all">
												{field.values.length > 0 ? field.values.join(', ') : '—'}
											</p>
											{#if field.error}
												<p class="text-danger">{field.error}</p>
											{/if}
										</div>
									{/each}
								</div>
							{:else if view === 'merge'}
								<div data-testid="ldap-panel-merge">
									{#each probeResult.merge ?? [] as merge (merge.name)}
										<div class="border-b border-border-subtle px-4 py-3 text-dense last:border-0">
											<p class="font-mono font-semibold">
												{merge.name}
												<span class="ml-2 font-sans text-xs text-ink-muted">{merge.action}</span>
											</p>
											{#if merge.note}
												<p class="text-ink-muted">{merge.note}</p>
											{/if}
											{#if merge.kept?.length}
												<p class="mt-1 flex flex-wrap gap-1.5">
													{#each merge.kept as value (value)}
														<MonoChip>{value}</MonoChip>
													{/each}
												</p>
											{/if}
											{#if merge.dropped?.length}
												<p class="mt-1 flex flex-wrap items-baseline gap-1.5">
													<span class="text-trimmed">dropped:</span>
													{#each merge.dropped as value (value)}
														<MonoChip>{value}</MonoChip>
													{/each}
												</p>
											{/if}
										</div>
									{/each}
									<p class="bg-surface-muted px-4 py-2 text-dense text-ink-muted">
										Nothing was written. No directory record, no group rows, no miss window, no
										auto-disable.
									</p>
								</div>
							{/if}

							{#if probeResult.suggestions?.length}
								<div class="border-t border-border-subtle px-4 py-3" data-testid="ldap-suggestions">
									<SectionLabel>Config that would keep what is being ignored</SectionLabel>
									<p class="mb-2 text-dense text-ink-muted">
										Suggestions, not decisions. Review each one before committing it.
									</p>
									{#each probeResult.suggestions as suggestion (suggestion.yaml)}
										<div class="mb-3 last:mb-0">
											<p class="text-dense text-ink-muted">{suggestion.reason}</p>
											<pre
												class="mt-1 overflow-x-auto rounded bg-surface-muted p-3 font-mono text-dense">{suggestion.yaml}</pre>
										</div>
									{/each}
								</div>
							{/if}
						{/if}
					</div>
				{/if}
			</div>
		</PageSection>

		{#if nssEnabled}
			<NSSStoredEntries />
			<NSSDirectoryPanels onannounce={(message) => (nssMessage = message)} />
		{/if}
	{/if}
</PageShell>
