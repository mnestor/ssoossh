<script lang="ts">
	import { ApiError } from '#lib/api/client.js';
	import { listRetrievals, setEnrollmentNotificationEmail } from '#lib/api/endpoints.js';
	import type { EnrollmentRetrievalsResponse, ServiceEnrollment } from '#lib/api/types.js';
	import { errorMessage } from '#lib/auth.js';
	import { session } from '#lib/session.svelte.js';
	import { detail, inDetailScope } from '#lib/detail.svelte.js';
	import AccountHoldersPanel from './AccountHoldersPanel.svelte';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import LoadingBlock from './LoadingBlock.svelte';
	import PageSection from './PageSection.svelte';
	import RedemptionHistory from './RedemptionHistory.svelte';
	import ServiceCodeFacts from './ServiceCodeFacts.svelte';

	// One service enrollment in full, the body of /service-codes/[id].
	// Deliberately unable to show the code: `service enroll` prints it once
	// and the server has no endpoint that returns one, so the answer here is
	// what the code grants and how long it lasts.
	//
	// The one thing here that can be changed is where notifications about the
	// code go. Everything else was fixed at approval, and the code belongs to
	// its service account, so there is no owner to transfer it to. Retiring
	// it is the page's own action, in the heading's top right, because it is
	// the one thing here that ends the code rather than describing it.
	//
	// A page rather than the dialog this used to be: this is a screenful of
	// fields, a redemption log and two controls, which is a page's worth of
	// reading — and inside a dialog none of it could be linked to, reloaded,
	// or reached with the browser's own Back.
	interface Props {
		enrollment: ServiceEnrollment;
		/** Pinned clock, so the remaining lifetime matches the list it came from. */
		now?: Date;
	}

	let { enrollment, now = new Date() }: Props = $props();

	let retrievals = $state<EnrollmentRetrievalsResponse | null>(null);
	// Scoped like every piece of a covered page: off one, it is always full.
	const covered = inDetailScope();
	const simplified = $derived(covered && detail.simplified);
	// The log's own states, apart from the page's: it is one section of a
	// detail that stands without it.
	let retrievalsLoading = $state(false);
	let retrievalsFailed = $state(false);
	// Bumped by Try again; the log load reads it, so a bump re-runs it.
	let retrievalsAttempt = $state(0);

	// The address form's own state. emailDraft is reset from the enrollment
	// whenever a different code is shown, so a draft abandoned on one code
	// does not follow the reader to the next.
	let emailDraft = $state('');
	let savingEmail = $state(false);
	let emailError = $state<string | null>(null);
	let emailSaved = $state(false);
	let storedEmail = $state('');

	// Both assignments read the prop, never storedEmail: an effect that read
	// the state it also writes would re-run on its own save and wipe the
	// "Saved." line it had just earned.
	$effect(() => {
		const address = enrollment.notification_email ?? '';
		storedEmail = address;
		emailDraft = address;
		emailError = null;
		emailSaved = false;
	});

	const emailDirty = $derived(emailDraft.trim() !== storedEmail);

	/** saveNotificationEmail stores the address, or clears it when empty. */
	async function saveNotificationEmail() {
		savingEmail = true;
		emailError = null;
		emailSaved = false;
		try {
			const stored = await setEnrollmentNotificationEmail(enrollment.id, emailDraft.trim());
			// Rendered from the answer rather than the draft: the server trims,
			// and the page should show what is actually stored.
			storedEmail = stored.notification_email;
			emailDraft = storedEmail;
			emailSaved = true;
		} catch (cause) {
			emailError = errorMessage(cause);
		} finally {
			savingEmail = false;
		}
	}

	// The redemption log, keyed on the request the enrollment was approved
	// from. The row already carries the count and the last redemption; this
	// is the detail behind them — when, from where, and whether a certificate
	// actually came out.
	$effect(() => {
		void retrievalsAttempt;
		retrievals = null;
		retrievalsFailed = false;

		const requestID = enrollment.certificate_request_id;
		if (!requestID) {
			return;
		}

		const controller = new AbortController();
		retrievalsLoading = true;

		listRetrievals(requestID, controller.signal)
			.then((result) => {
				retrievals = result;
				retrievalsLoading = false;
			})
			.catch((cause) => {
				if (controller.signal.aborted) {
					return;
				}
				retrievalsLoading = false;
				// A 404 (no enrollment for that request) or 403 (someone else's)
				// is an answer, not a failure: the summary counts on the row
				// stand on their own, so the log is simply omitted. Anything
				// else, a network failure (the client's ApiError with status
				// 0) above all, used to look the same and is not, so it says
				// so and offers the read again.
				const refused = cause instanceof ApiError && (cause.isNotFound || cause.isForbidden);
				if (!refused) {
					retrievalsFailed = true;
				}
			});

		return () => controller.abort();
	});

	// Named here as well as inside ServiceCodeFacts, because the notification
	// copy below is a sentence about the account rather than a field: "go to
	// everyone with access to svc-deploy".
	// How many redemptions the simplified page lists.
	const recentRedemptions = 5;

	const subject = $derived(
		enrollment.service_account ||
			(enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account')
	);
</script>

<div class="flex flex-col gap-8" data-testid="service-code-detail">
	<ServiceCodeFacts {enrollment} {now} approvedBy={enrollment.approved_by_username ?? ''} />

	<!-- Immediately above the notification address, because the two answer
	     the same question from opposite ends: this is who the code reaches by
	     default, and the field below is how to send it somewhere else
	     instead. -->
	{#if !simplified}
		<AccountHoldersPanel
			enrollmentId={enrollment.id}
			serviceAccount={enrollment.service_account}
			viewerUsername={session.user?.username ?? ''}
		/>
	{/if}

	<!-- The only editable thing here. It exists for the cases fan-out cannot
	     serve: an account whose holders have never logged in reaches nobody,
	     and a large holder set turns every redemption into a mailshot where a
	     team alias would do. -->
	<PageSection title="Notifications">
		<p class="mb-2 text-dense text-ink-muted" id="notification-email-help-{enrollment.id}">
			{#if storedEmail}
				Notifications about this code go to
				<span class="font-mono">{storedEmail}</span>. Clear the field to send them to everyone with
				access to the account instead.
			{:else}
				Notifications about this code go to everyone with access to
				<span class="font-mono">{subject}</span>. Set an address to send them to one place instead.
			{/if}
		</p>
		<div class="flex max-w-140 flex-wrap items-start gap-2">
			<label class="flex min-w-55 flex-1 flex-col gap-1">
				<span class="text-dense font-medium text-ink">Send notifications to</span>
				<input
					type="email"
					bind:value={emailDraft}
					data-testid="notification-email-input"
					placeholder="deploys@example.com"
					disabled={savingEmail}
					aria-describedby="notification-email-help-{enrollment.id}"
					class="min-h-target rounded-md border border-border-control bg-surface px-3 py-2 text-dense text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
				/>
			</label>
			<Button
				testid="notification-email-save"
				busy={savingEmail}
				disabled={!emailDirty}
				onclick={saveNotificationEmail}
			>
				{savingEmail ? 'Saving…' : 'Save'}
			</Button>
		</div>
		{#if emailError}
			<div class="mt-2">
				<Alert variant="error" title="That did not save" testid="notification-email-error">
					{emailError}
				</Alert>
			</div>
		{/if}
		<!-- Always in the document, so a screen reader hears the confirmation:
		     a status region that appears with its text already in it is often
		     not announced at all. -->
		<p class="mt-2 text-dense text-granted" role="status" data-testid="notification-email-saved">
			{#if emailSaved && !emailError}
				{emailDraft ? 'Saved.' : 'Cleared — notifications go to everyone with access again.'}
			{/if}
		</p>
	</PageSection>

	<!-- Last, as on the admin's page. It is the only part that grows without
	     bound, and a reader who came to check what the code grants or to
	     retire it should not have to scroll past a year of cron redemptions
	     to reach either. Absent entirely when the log did not load — a 404 or
	     someone else's request — because the facts above stand without it. -->
	<!-- At the simplified level, the latest few rather than just the count:
	     when and from where a code was last redeemed is how a holder notices
	     it being used from somewhere unexpected, and a count cannot show
	     that. The full log stays at the advanced level. -->
	{#if retrievals}
		<RedemptionHistory
			retrievals={simplified
				? retrievals.retrievals.slice(0, recentRedemptions)
				: retrievals.retrievals}
			total={retrievals.total}
			title={simplified ? 'Recent redemptions' : 'Redemption history'}
		/>
	{:else if retrievalsLoading}
		<PageSection title={simplified ? 'Recent redemptions' : 'Redemption history'}>
			<LoadingBlock
				shape="lines"
				count={3}
				label="Loading the redemption history…"
				testid="redemption-loading"
			/>
		</PageSection>
	{:else if retrievalsFailed}
		<PageSection title={simplified ? 'Recent redemptions' : 'Redemption history'}>
			<Alert
				variant="error"
				title="Could not load the redemption history"
				onretry={() => (retrievalsAttempt += 1)}
				testid="redemption-error"
			>
				The rest of this code's details are current.
			</Alert>
		</PageSection>
	{/if}
</div>
