<script lang="ts">
	import { getBranding } from '$lib/branding.svelte';

	// The mark that sits to the left of the "ssoossh" wordmark and above the
	// login heading. It is the deployment logo slot: a deployment that sets
	// logo_url gets its own image here, everything else gets ssoossh's own
	// mark, so the slot is never empty.
	//
	// The default mark is a shell prompt and a key inside a cloud: the two
	// halves of what the product does, single sign-on and an SSH session,
	// in the one glyph. It is drawn to Lucide's conventions (a 24x24 box,
	// round caps and joins, stroke only) so it sits with the rail's icons
	// rather than beside them, and its geometry is centred in that box —
	// every stroke keeps at least 2.5 units of clearance from the cloud
	// wall, which is where 1.75-wide strokes start to close up.
	interface Props {
		/** Rendered edge length in pixels. */
		size?: number;
		/** Stroke weight of the default mark; thinner reads better when large. */
		strokeWidth?: number;
	}

	let { size = 22, strokeWidth = 1.75 }: Props = $props();

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
		<path
			d="M3.6 19 A6 6 0 0 1 4.2 9.6 A4.4 4.4 0 0 1 11.5 6 A4.1 4.1 0 0 1 17.8 8.8 A6.6 6.6 0 0 1 20.8 19 Z"
		/>
		<polyline points="7 11.2 9.5 13.1 7 15" />
		<circle cx="13.2" cy="15" r="1.5" />
		<line x1="14.7" y1="15" x2="19.2" y2="15" />
		<line x1="17.6" y1="15" x2="17.6" y2="12.8" />
	</svg>
{/if}
