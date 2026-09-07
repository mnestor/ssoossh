<script lang="ts">
	import type { RequestStatus } from '$lib/api/types';
	import Icon from './Icon.svelte';

	interface Props {
		status: RequestStatus;
	}

	let { status }: Props = $props();

	// Every status gets an explicit style so a new one added server-side
	// shows up as unstyled-but-visible rather than silently matching some
	// unrelated case.
	const styles: Record<RequestStatus, string> = {
		pending: 'bg-trimmed-surface text-trimmed',
		signing: 'bg-trimmed-surface text-trimmed',
		approved: 'bg-granted-surface text-granted',
		enrolled: 'bg-granted-surface text-granted',
		denied: 'bg-danger-surface text-danger',
		expired: 'bg-surface-muted text-ink-muted',
		failed: 'bg-danger-surface text-danger'
	};

	// Seven states, seven glyphs. Approved and enrolled used to share a
	// tick and denied and failed used to share a cross, which hid the two
	// distinctions a reader most needs: approved means a certificate
	// exists where enrolled means a code exists that nothing has redeemed
	// yet, and denied is a decision to appeal where failed is a signer
	// fault to retry. `clock-cancel` keeps the circle of a clock face
	// while saying the window closed, which is what expired means here —
	// nobody refused the request, nobody answered it.
	const iconMap: Record<RequestStatus, string> = {
		pending: 'hourglass-high',
		signing: 'loader-2',
		approved: 'circle-check',
		enrolled: 'circle-key',
		denied: 'circle-x',
		expired: 'clock-cancel',
		failed: 'alert-octagon'
	};

	// Signing is the one transient state in the set, and a motionless
	// spinner reads as stuck. motion-safe keeps it still for anyone who
	// asked the OS for reduced motion.
	const spin = $derived(status === 'signing' ? 'motion-safe:animate-spin' : '');
</script>

<span
	class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold capitalize {styles[
		status
	] ?? 'bg-surface-muted text-ink-muted'}"
>
	<Icon name={iconMap[status]} size="xs" class={spin} />
	{status}
</span>
