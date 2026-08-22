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

	const iconMap: Record<RequestStatus, string> = {
		pending: 'clock',
		signing: 'loader',
		approved: 'check-circle',
		enrolled: 'check-circle',
		denied: 'x-circle',
		expired: 'alert-triangle',
		failed: 'x-circle'
	};
</script>

<span
	class="inline-flex flex-shrink-0 items-center gap-1.5 rounded-full px-2.5 py-1 text-xs font-semibold capitalize {styles[
		status
	] ?? 'bg-surface-muted text-ink-muted'}"
>
	<Icon name={iconMap[status]} size="xs" />
	{status}
</span>
