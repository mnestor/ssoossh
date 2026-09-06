<script lang="ts">
	import { ApiError } from '$lib/api/client';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';

	// Retiring a service enrollment code, from either side of it: an admin
	// on the admin panel, or somebody who holds the account on their own.
	// One component because the two differ in exactly one thing — which
	// endpoint they call — and the reason field, the confirmation step and
	// the error wording are the parts worth keeping identical.
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
	let reason = $state('');
	let busy = $state(false);
	let error = $state<string | null>(null);

	const ready = $derived(reason.trim().length > 0);

	function cancel() {
		confirming = false;
		reason = '';
		error = null;
	}

	async function run() {
		busy = true;
		error = null;
		try {
			await expire(reason.trim());
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
	{#if !confirming}
		<Button variant="danger" onclick={() => (confirming = true)}>Expire this code</Button>
	{:else}
		<div class="flex flex-col gap-2 rounded-lg bg-danger-surface p-3">
			<p class="text-[13px] text-ink">
				Nothing will be able to redeem this code again. Certificates already issued keep working
				until they expire on their own.
			</p>

			<!-- Required, and said so before the button is pressed rather than
			     after the server refuses: the reason is what the next person
			     reading this code's history has to go on. -->
			<label class="flex flex-col gap-1 text-[13px]">
				<span class="font-medium text-ink">Why is it being retired?</span>
				<input
					type="text"
					bind:value={reason}
					disabled={busy}
					data-testid="expire-reason"
					placeholder="the job behind it was decommissioned"
					class="rounded-md border border-border-subtle bg-surface px-2.5 py-1.5 text-[13px] disabled:opacity-50"
				/>
			</label>

			<div class="flex gap-2">
				<Button variant="danger" disabled={busy || !ready} onclick={run} testid="expire-confirm">
					{busy ? 'Retiring…' : 'Confirm expiry'}
				</Button>
				<Button variant="ghost" disabled={busy} onclick={cancel}>Cancel</Button>
			</div>

			{#if error}
				<Alert variant="error" title="The code was not retired">{error}</Alert>
			{/if}
		</div>
	{/if}
</div>
