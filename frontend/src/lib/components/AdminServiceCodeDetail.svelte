<script lang="ts">
	import { setEnrollmentNotificationEmail } from '$lib/api/endpoints';
	import type { AdminEnrollmentDetail } from '$lib/api/endpoints';
	import { errorMessage } from '$lib/auth';
	import AccountHoldersPanel from './AccountHoldersPanel.svelte';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import PageSection from './PageSection.svelte';
	import RedemptionHistory from './RedemptionHistory.svelte';
	import ServiceCodeFacts from './ServiceCodeFacts.svelte';

	// One enrollment as an operator sees it, the body of
	// /admin/service-codes/[id].
	//
	// The detail is handed in rather than fetched here: GET
	// /api/admin/enrollments/:id is audited, so a component that fetched on
	// mount would write a second admin.enrollment_viewed event for the one
	// look the route has already recorded.
	//
	// A page rather than the dialog this used to be. The list opens a code by
	// navigating to it, which is what makes an id from a log line, a
	// notification or an audit event openable at all — the dialog could only
	// ever be reached by finding the row again.
	interface Props {
		detail: AdminEnrollmentDetail;
		now?: Date;
	}

	let { detail, now = new Date() }: Props = $props();

	const enrollment = $derived(detail.enrollment);

	// The notification address, editable here for the deployment where the
	// account's holders are outside ssoossh entirely and cannot set it
	// themselves. Reset per enrollment so a draft does not follow the reader
	// from one code to the next.
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
			// From the answer, not the draft: the server trims, and the page
			// should show what is actually stored.
			storedEmail = stored.notification_email;
			emailDraft = storedEmail;
			emailSaved = true;
		} catch (cause) {
			emailError = errorMessage(cause);
		} finally {
			savingEmail = false;
		}
	}

	// The approver in full. An operator reading an audit trail needs to be
	// able to tell two people with similar usernames apart, which is the one
	// place this audience is shown more than the holder's page shows.
	const approvedBy = $derived(
		`${enrollment.approved_by_username} (${enrollment.approved_by_email})`
	);
</script>

<div class="flex flex-col gap-5" data-testid="service-code-detail">
	<ServiceCodeFacts {enrollment} {now} {approvedBy} />

	<!-- Immediately above the admin controls, because the first of them
	     redirects notifications away from exactly these people. Provenance is
	     the "Approved by" row above; this is ownership. -->
	<AccountHoldersPanel enrollmentId={enrollment.id} serviceAccount={enrollment.service_account} />

	<!-- Admin controls, in their own card like every other block: what only
	     an operator can do is separated from what any holder sees by being a
	     section of its own. Retiring the code is not among them — that is the
	     page's action, in the heading's top right, the same place an account
	     is disabled from. -->
	<PageSection title="Admin actions">
		<!-- The address is editable here as well as on the holder's own
			     page, for the deployment where the account's holders are
			     outside ssoossh entirely and so have no page to set it on.
			     Changing it is audited. -->
		<div class="space-y-2">
			<p class="text-dense text-ink-muted">
				{#if storedEmail}
					Notifications about this code go to
					<span class="font-mono">{storedEmail}</span>. Clear the field to send them to everyone
					with access to the account instead.
				{:else}
					Notifications about this code go to everyone with access to the account. Set an address to
					send them to one place instead.
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
						class="rounded border border-border-control bg-surface px-3 py-2 text-dense text-ink focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent"
					/>
				</label>
				<Button
					testid="notification-email-save"
					busy={savingEmail}
					disabled={!emailDirty}
					onclick={saveNotificationEmail}
				>
					{savingEmail ? 'Saving…' : 'Save address'}
				</Button>
			</div>
			{#if emailError}
				<Alert variant="error" title="That did not save" testid="notification-email-error">
					{emailError}
				</Alert>
			{:else if emailSaved}
				<p class="text-dense text-granted" data-testid="notification-email-saved">
					{emailDraft ? 'Saved.' : 'Cleared — notifications go to everyone with access again.'}
				</p>
			{/if}
		</div>
	</PageSection>

	<!-- Last, as on the holder's page. It is the only part that grows without
	     bound — a year of an hourly cron's redemptions — and an operator who
	     opened the code to read what it grants or to expire it should not have
	     to scroll past all of it to reach either. -->
	<RedemptionHistory retrievals={detail.retrievals} total={detail.retrieval_total} />
</div>
