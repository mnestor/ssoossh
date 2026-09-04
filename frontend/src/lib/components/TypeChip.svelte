<script lang="ts">
	import type { CertificateType } from '$lib/api/types';
	import Icon from './Icon.svelte';

	// The labelled form of TypeBadge, for detail views where there is room to
	// name the type rather than rely on the icon alone.
	interface Props {
		type: CertificateType;
	}

	let { type }: Props = $props();

	const icons: Record<CertificateType, string> = {
		user: 'user',
		pam: 'terminal',
		service: 'cog',
		console: 'monitor'
	};

	const labels: Record<CertificateType, string> = {
		user: 'User',
		pam: 'PAM',
		service: 'Service',
		console: 'Console'
	};
</script>

<span
	class="inline-flex w-fit flex-shrink-0 items-center gap-1 rounded border border-border-subtle px-[7px] py-[3px] text-[10px] font-semibold tracking-[0.05em] text-ink-muted uppercase"
>
	<Icon name={icons[type] ?? 'zap'} size="xs" class="h-[11px] w-[11px]" />
	{labels[type] ?? type}
</span>
