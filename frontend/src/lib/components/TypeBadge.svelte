<script lang="ts">
	import type { CertificateType } from '#lib/api/types.js';
	import Icon from './Icon.svelte';

	// The certificate type as a mark in a fixed-size box. Fixed rather than
	// content-sized so rows line up vertically however long the type's name
	// is, and always shown rather than gated behind an icon-scope preference:
	// on a list row the type is the primary identifier, not decoration.
	//
	// No fill and no radius: a tinted rounded square reads as an icon
	// button, and this one does nothing. Nor is it the type's only name:
	// every row that carries it says the type in words on its detail line
	// (certificateEvent, or the row's own text), because a glyph that needs
	// a hover to be understood has failed (DESIGN.md, Iconography).
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

<!-- title as well as the hidden name, as a fallback for the pointer only:
     it shows late and never on touch or keyboard focus, so the row's own
     words are the name and this is not. -->
<span
	class="inline-flex size-6.5 flex-shrink-0 items-center justify-center text-ink-muted"
	title={(type && (labels[type] ?? type)) || 'Unknown type'}
>
	<Icon name={(type && icons[type]) || 'help-circle'} size="sm" />
	<!-- The name in the document rather than an `aria-label` on the span.
	     ARIA prohibits naming the `generic` role, which is what a bare span
	     has, so the label this badge used to carry was discarded by every
	     browser — leaving the one identifier a history row leads with with
	     no text alternative at all. A real (hidden) string cannot be
	     dropped, and it survives the span becoming a div or a flex wrapper
	     later, which `role="img"` would not. -->
	<span class="sr-only">Certificate type: {(type && (labels[type] ?? type)) || 'unknown'}</span>
</span>
