<script lang="ts">
	import type { Snippet } from 'svelte';

	// The one container every page sits in.
	//
	// Before this, each page hand-rolled its own `flex w-full max-w-[Npx]
	// flex-col gap-N`, and twenty-one of them between them named nine
	// different widths — 380, 560, 600, 680, 1100, max-w-2xl, max-w-md,
	// max-w-full. The admin layout pinned 1100px on top, so the pages inside
	// it settled their width in two places that disagreed. Naming the four
	// widths that were actually meant is what stops the tenth from appearing.
	//
	// `full` is not "very wide": it opts out of the cap entirely, for the
	// three table pages where a horizontal scrollbar inside a centred column
	// is worse than using the glass.
	export type PageWidth = 'focus' | 'default' | 'wide' | 'full';

	interface Props {
		/**
		 * focus   560px — sign-in, an approval, a console code
		 * default 760px — reading pages that stand on their own
		 * wide   1120px — card and row lists, and the pages they open
		 * full          — uncapped, for tables
		 *
		 * A page reached by opening a row takes its list's width rather than
		 * the narrower reading one. Both are centred in the same space, so a
		 * detail page 360px narrower than the list behind it moves the left
		 * edge inward by half that on every click — the content shifts and a
		 * gap opens beside it, which reads as a different app rather than as
		 * the next screen. Long values are the reason 760 exists, and a
		 * DetailRow does not stretch them: its value starts right after the
		 * 140px label column and only wraps later. Forms are the exception
		 * and cap themselves — an email field 900px wide looks like a bug.
		 */
		width?: PageWidth;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children: Snippet;
		/**
		 * An optional secondary column: filters, a raw payload, anything
		 * that accompanies the page without being it. It sits beside the
		 * main column above `xl` and stacks under it below, where a 280px
		 * sidebar would leave neither column usable.
		 */
		aside?: Snippet;
		/**
		 * Centre the page in the viewport's remaining height.
		 *
		 * One screen wants this — sign-in, which is a single card with
		 * nothing above or below it and looks abandoned pinned to the top of
		 * a tall window. Every other page starts at the top, because a page
		 * whose vertical position depends on how much content it happens to
		 * have is a page that moves when it loads.
		 */
		center?: boolean;
	}

	let { width = 'default', testid, children, aside, center = false }: Props = $props();

	// Written out per width rather than interpolated: Tailwind scans source
	// text for literal class names, so `max-w-[${n}px]` would never be
	// generated.
	const caps: Record<PageWidth, string> = {
		focus: 'max-w-[560px]',
		default: 'max-w-[760px]',
		wide: 'max-w-[1120px]',
		full: 'max-w-none'
	};
</script>

<div
	data-testid={testid}
	data-page-width={width}
	class="flex w-full flex-col"
	class:flex-1={center}
	class:justify-center={center}
>
	<div class="mx-auto w-full {caps[width]}">
		{#if aside}
			<div class="flex flex-col gap-8 xl:flex-row xl:items-start">
				<div class="flex min-w-0 flex-1 flex-col gap-5">
					{@render children()}
				</div>
				<aside class="flex w-full flex-col gap-5 xl:w-[280px] xl:shrink-0">
					{@render aside()}
				</aside>
			</div>
		{:else}
			<div class="flex flex-col gap-5">
				{@render children()}
			</div>
		{/if}
	</div>
</div>
