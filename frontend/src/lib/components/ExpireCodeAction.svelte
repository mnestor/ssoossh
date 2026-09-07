<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import Button from './Button.svelte';
	import ConfirmModal from './ConfirmModal.svelte';

	// Retiring a service enrollment code, from either side of it: an admin
	// on the admin panel, or somebody who holds the account on their own.
	// One component because the two differ in exactly one thing — which
	// endpoint they call — and the reason field, the confirmation step and
	// the error wording are the parts worth keeping identical.
	//
	// A button meant for a page's top right, opening the same ConfirmModal
	// that disabling an account opens. It used to expand a panel inline in a
	// section near the foot of the page, which put the control that ends a
	// code wherever the sections above it happened to finish — and left the
	// reason field competing with the page behind it while it was being
	// typed.
	//
	// The reason is not optional decoration. enrollment.expired is one of
	// the three actions the server validates a reason for, so a request
	// without one is refused. The admin panel used to send no body at all,
	// which meant its Expire button answered 400 every time it was pressed.
	interface Props {
		/** Performs the expiry. The caller picks the endpoint. */
		expire: (reason: string) => Promise<unknown>;
		/** Called once the code is retired, to close whatever is showing. */
		onexpired: () => void;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { expire, onexpired, testid }: Props = $props();

	let confirming = $state(false);
	let busy = $state(false);
	let error = $state<string | null>(null);

	function cancel() {
		confirming = false;
		error = null;
	}

	async function run(reason: string) {
		busy = true;
		error = null;
		try {
			await expire(reason);
			onexpired();
		} catch (cause) {
			if (cause instanceof ApiError && cause.status === 404) {
				error = 'This code no longer exists.';
			} else if (cause instanceof ApiError && cause.status === 403) {
				error = 'You do not hold the service account this code belongs to.';
			} else {
				error = cause instanceof Error ? cause.message : 'The code could not be retired.';
			}
		} finally {
			busy = false;
		}
	}
</script>

<div data-testid={testid}>
	<Button variant="danger" onclick={() => (confirming = true)}>Expire this code</Button>

	{#if confirming}
		<ConfirmModal
			title="Expire this code?"
			confirmLabel="Confirm expiry"
			busyLabel="Retiring…"
			{busy}
			{error}
			errorTitle="The code was not retired"
			reasonLabel="Why is it being retired?"
			reasonPlaceholder="the job behind it was decommissioned"
			reasonHelp="Shown to whoever next reads this code's history."
			reasonTestid="expire-reason"
			confirmTestid="expire-confirm"
			onconfirm={run}
			oncancel={cancel}
		>
			Nothing will be able to redeem this code again. Certificates already issued keep working until
			they expire on their own.
		</ConfirmModal>
	{/if}
</div>
