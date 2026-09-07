<script lang="ts">
	import type { CertificateType } from '$lib/api/types';
	import Icon from './Icon.svelte';

	// The certificate type as a fixed-size square. Fixed rather than
	// content-sized so rows line up vertically however long the type's name
	// is, and always shown rather than gated behind an icon-scope preference:
	// on a list row the type is the primary identifier, not decoration.
	interface Props {
		/**
		 * Undefined only for a denial whose request row has gone: the
		 * decisions table outlives certificate_requests by design, so what
		 * was asked for is not always still knowable. The badge says so
		 * rather than picking a type, since an invented one is a wrong
		 * answer where a blank is a missing one.
		 */
		type?: CertificateType;
	}

	let { type }: Props = $props();

	// Four rectilinear objects, so the family reads as one group before
	// any single glyph is recognised: a person's badge, a terminal, a
	// server doing a job, a screen.
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
	aria-label="Certificate type: {(type && (labels[type] ?? type)) || 'unknown'}"
	class="inline-flex h-[26px] w-[26px] flex-shrink-0 items-center justify-center rounded-md border border-border-subtle text-ink-muted"
>
	<Icon name={(type && icons[type]) || 'help-circle'} size="xs" />
</span>
