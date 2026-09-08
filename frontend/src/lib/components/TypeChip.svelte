<script lang="ts">
	import type { CertificateType } from '$lib/api/types';
	import Icon from './Icon.svelte';

	// The labelled form of TypeBadge, for detail views where there is room to
	// name the type rather than rely on the icon alone.
	interface Props {
		type: CertificateType;
	}

	let { type }: Props = $props();

	// The same four glyphs TypeBadge carries; see the note there.
	const icons: Record<CertificateType, string> = {
		user: 'id-badge',
		pam: 'terminal-2',
		service: 'server-cog',
		console: 'device-desktop'
	};

	const labels: Record<CertificateType, string> = {
		user: 'User',
		pam: 'PAM',
		service: 'Service',
		console: 'Console'
	};
</script>

<span
	class="inline-flex w-fit flex-shrink-0 items-center gap-1 rounded border border-border-subtle px-2 py-0.5 text-micro font-semibold tracking-label text-ink-muted uppercase"
>
	<Icon name={icons[type] ?? 'help-circle'} size="xs" />
	{labels[type] ?? type}
</span>
