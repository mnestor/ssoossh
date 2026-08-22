<script lang="ts">
	import { getBranding } from '$lib/branding.svelte';

	// The mark that sits to the left of the "ssoossh" wordmark and above the
	// login heading. It is the deployment logo slot: a deployment that sets
	// logo_url gets its own image here, everything else gets ssoossh's own
	// mark, so the slot is never empty.
	interface Props {
		/** Rendered edge length in pixels. */
		size?: number;
		/** Stroke weight of the default mark; thinner reads better when large. */
		strokeWidth?: number;
	}

	let { size = 22, strokeWidth = 2 }: Props = $props();

	const branding = $derived(getBranding());

	// Corner rounding tracks the mark's size (~22% of the edge) so a 22px
	// header mark and a 40px login mark read as the same shape.
	const radius = $derived(Math.round(size * 0.22));
</script>

{#if branding.logo_url}
	<!-- Height-constrained with flexible width: most organisation logos
	     are wide wordmarks, and forcing them into a square box renders
	     them illegible. max-w caps a pathological aspect ratio. -->
	<img
		src={branding.logo_url}
		alt={branding.org_name ? `${branding.org_name} logo` : 'Organization logo'}
		class="w-auto max-w-40 object-contain"
		style="height: {size}px; border-radius: {radius}px"
	/>
{:else}
	<svg
		width={size}
		height={size}
		viewBox="0 0 24 24"
		fill="none"
		stroke="currentColor"
		stroke-width={strokeWidth}
		stroke-linecap="round"
		stroke-linejoin="round"
		class="block flex-shrink-0 text-accent"
		aria-hidden="true"
	>
		<circle cx="12" cy="12" r="9" />
		<polyline points="8 12 11 15 16 9" />
	</svg>
{/if}
