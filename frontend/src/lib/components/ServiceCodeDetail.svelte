<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import { listRetrievals, setEnrollmentNotificationEmail } from '$lib/api/endpoints';
	import type { EnrollmentRetrievalsResponse, ServiceEnrollment } from '$lib/api/types';
	import { errorMessage } from '$lib/auth';
	import { session } from '$lib/session.svelte';
	import AccountHoldersPanel from './AccountHoldersPanel.svelte';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
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
		retrievals = null;

		const requestID = enrollment.certificate_request_id;
		if (!requestID) {
			return;
		}

		const controller = new AbortController();

		listRetrievals(requestID, controller.signal)
			.then((result) => {
				retrievals = result;
			})
			.catch((cause) => {
				if (controller.signal.aborted) {
					return;
				}
				// A 404 (no enrollment for that request) or 403 (someone else's)
				// is not an error worth a banner: the summary counts on the row
				// stand on their own, and the rest of the page is unaffected.
				if (!(cause instanceof ApiError)) {
					return;
				}
				retrievals = null;
			});

		return () => controller.abort();
	});

	// Named here as well as inside ServiceCodeFacts, because the notification
	// copy below is a sentence about the account rather than a field: "go to
	// everyone with access to svc-deploy".
	const subject = $derived(
		enrollment.service_account ||
			(enrollment.principals.length > 0 ? enrollment.principals.join(', ') : 'unknown account')
	);
</script>

<div class="flex flex-col gap-5" data-testid="service-code-detail">
	<ServiceCodeFacts {enrollment} {now} approvedBy={enrollment.approved_by_username ?? ''} />

	<!-- Immediately above the notification address, because the two answer
	     the same question from opposite ends: this is who the code reaches by
	     default, and the field below is how to send it somewhere else
	     instead. -->
	<AccountHoldersPanel
		enrollmentId={enrollment.id}
		serviceAccount={enrollment.service_account}
		viewerUsername={session.user?.username ?? ''}
	/>

	<!-- The only editable thing here. It exists for the cases fan-out cannot
	     serve: an account whose holders have never logged in reaches nobody,
	     and a large holder set turns every redemption into a mailshot where a
	     team alias would do. -->
	<PageSection title="Notifications">
		<p class="mb-2 text-[13px] text-ink-muted">
			{#if storedEmail}
				Notifications about this code go to
				<span class="font-mono">{storedEmail}</span>. Clear the field to send them to everyone with
				access to the account instead.
			{:else}
				Notifications about this code go to everyone with access to
				<span class="font-mono">{subject}</span>. Set an address to send them to one place instead.
			{/if}
		</p>
		<div class="flex max-w-[560px] flex-wrap items-start gap-2">
			<label class="flex min-w-[220px] flex-1 flex-col gap-1">
				<span class="sr-only">Notification address</span>
				<input
					type="email"
					bind:value={emailDraft}
					data-testid="notification-email-input"
					placeholder="deploys@example.com"
					disabled={savingEmail}
					class="rounded border border-border-subtle bg-surface px-3 py-2 text-[13px] text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
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
		{:else if emailSaved}
			<p class="mt-2 text-[13px] text-granted" data-testid="notification-email-saved">
				{emailDraft ? 'Saved.' : 'Cleared — notifications go to everyone with access again.'}
			</p>
		{/if}
	</PageSection>

	<!-- Last, as on the admin's page. It is the only part that grows without
	     bound, and a reader who came to check what the code grants or to
	     retire it should not have to scroll past a year of cron redemptions
	     to reach either. Absent entirely when the log did not load — a 404 or
	     someone else's request — because the facts above stand without it. -->
	{#if retrievals}
		<RedemptionHistory retrievals={retrievals.retrievals} total={retrievals.total} />
	{/if}
</div>
