<script lang="ts">
	import type { CertificateType } from '$lib/api/types';
	import Icon from './Icon.svelte';

	// The certificate type as a fixed-size square. Fixed rather than
	// content-sized so rows line up vertically however long the type's name
	// is, and always shown rather than gated behind an icon-scope preference:
	// on a list row the type is the primary identifier, not decoration.
	interface Props {
		type: CertificateType;
	}

	let { type }: Props = $props();

	const icons: Record<CertificateType, string> = {
		user: 'user',
		pam: 'terminal',
		service: 'cog'
	};

	const labels: Record<CertificateType, string> = {
		user: 'User',
		pam: 'PAM',
		service: 'Service'
	};
</script>

<span
	aria-label="Certificate type: {labels[type] ?? type}"
	class="inline-flex h-[26px] w-[26px] flex-shrink-0 items-center justify-center rounded-md border border-border-subtle text-ink-muted"
>
	<Icon name={icons[type] ?? 'zap'} size="xs" />
</span>
