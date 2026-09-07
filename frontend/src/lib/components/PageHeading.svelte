<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	// Every screen opens the same way, and this is the whole of it: an
	// optional back chip, a small accent eyebrow naming the area, the page's
	// own h1, an optional line under it, and an optional trailing control.
	//
	// All four parts live here because every one of them had been rebuilt by
	// hand somewhere. The back chip was the same nine-class string copied
	// onto three pages as an <a> and a fourth as a <button>, each with a
	// `-mb-2` cancelling the shell's gap. The sub line existed in four
	// shapes: this snippet, a `-mt-2 text-sm` paragraph, a `-mt-2 text-[13px]`
	// one, and a `text-sm` one with no pull at all. And five pages that had
	// no eyebrow wrote out the h1's own four classes rather than use the
	// component, so a change to the heading scale would have moved most of
	// the app and missed those.
	//
	// The eyebrow is required rather than optional: it is what makes a
	// screen identifiable at a glance without reading the title, and making
	// it optional is exactly how those five pages came to have none.
	interface Props {
		/** Short area name — "Activity", "History", "Certificate request". */
		eyebrow: string;
		title: string;
		/**
		 * An optional line under the title. A snippet rather than a string
		 * because several of them are not prose: a user detail page names
		 * the account in mono beside its address.
		 */
		sub?: Snippet;
		/** Optional trailing control, right-aligned against the title. */
		action?: Snippet;
		/**
		 * The chip above the eyebrow, naming where this page was opened
		 * from. `href` makes it a link — the usual case, so middle-click and
		 * "copy link address" work — and `onclick` makes it a button, for
		 * the one list that opens an account without changing route.
		 */
		back?: {
			label: string;
			href?: string;
			onclick?: () => void;
			/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
			testid?: string;
		};
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
	}

	let { eyebrow, title, sub, action, back, testid }: Props = $props();

	const chip =
		'inline-flex w-fit items-center gap-1 text-sm text-accent transition hover:underline focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent';
</script>

<div data-testid={testid} class="flex flex-col gap-2">
	{#if back}
		{#if back.href}
			<!-- The caller resolves its own route id, so a rename fails the
			     build there rather than 404ing here. Some of these carry a
			     search parameter as well, which resolve() does not take. -->
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
			<a href={back.href} data-testid={back.testid} class={chip}>
				<Icon name="chevron-left" size="xs" />
				{back.label}
			</a>
		{:else}
			<button type="button" onclick={back.onclick} data-testid={back.testid} class={chip}>
				<Icon name="chevron-left" size="xs" />
				{back.label}
			</button>
		{/if}
	{/if}

	<div class="flex items-center justify-between gap-4">
		<div class="min-w-0">
			<div class="mb-1.5 text-xs font-semibold tracking-[0.06em] text-accent uppercase">
				{eyebrow}
			</div>
			<h1 class="text-[26px] leading-tight font-bold tracking-[-0.01em]">{title}</h1>
			{#if sub}
				<p class="mt-1.5 text-sm text-ink-muted">{@render sub()}</p>
			{/if}
		</div>
		{#if action}
			<div class="shrink-0">{@render action()}</div>
		{/if}
	</div>
</div>
