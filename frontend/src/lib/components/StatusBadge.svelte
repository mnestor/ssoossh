<script lang="ts">
	import type { RequestStatus } from '$lib/api/types';

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
</script>

<span
	class="inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium {styles[status] ??
		'bg-surface-muted text-ink-muted'}"
>
	{status}
</span>
