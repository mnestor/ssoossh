<script lang="ts">
	import type { Snippet } from 'svelte';
	import Icon from './Icon.svelte';

	// Every screen opens the same way: an optional back chip naming where
	// this page was opened from, the page's own h1, an optional line under
	// it, and an optional trailing control.
	//
	// There used to be a fourth part between the chip and the h1 — a small
	// accent eyebrow naming the area. It went because on most pages it said
	// the title again in smaller letters ("Account" over "Your account",
	// "History" over "Certificate history", "Service" over "Service
	// enrollment codes"), and on a detail page it made a third line of
	// naming above the content: "All codes for svc-deploy", then "Service
	// code", then the account again as the h1. One page can only be in one
	// place, and the rail already says which section that is.
	//
	// What the eyebrow was carrying on the few pages where it carried
	// something real — the admin/user split on two identically titled lists,
	// the word "Certificate" over a page titled "Details" — moved into the
	// titles themselves, which is where a page's name belongs.
	//
	// The chip and the sub line live here because both had been rebuilt by
	// hand somewhere. The chip was the same nine-class string copied onto
	// three pages as an <a> and a fourth as a <button>, each with a `-mb-2`
	// cancelling the shell's gap. The sub line existed in four shapes: this
	// snippet, a `-mt-2 text-sm` paragraph, a `-mt-2 text-dense` one, and a
	// `text-sm` one with no pull at all.
	interface Props {
		/**
		 * The page's own name, and the only naming it gets. It has to stand
		 * on its own now that nothing sits above it: "Details" was a title
		 * only while an eyebrow said "Certificate" over it.
		 */
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
		 * The chip above the title, naming where this page was opened from.
		 * `href` makes it a link — the usual case, so middle-click and "copy
		 * link address" work — and `onclick` makes it a button, for the one
		 * list that opens an account without changing route.
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

	let { title, sub, action, back, testid }: Props = $props();

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
			<!-- 26px is a desktop size. On a phone the same title runs to two
			     or three lines and pushes the page's first real content off
			     the screen, so it steps down to 20px below `sm` — still the
			     largest thing on the page, which is all the h1 has to be. -->
			<h1 class="text-xl leading-tight font-bold tracking-heading sm:text-display">{title}</h1>
			{#if sub}
				<p class="mt-1.5 text-sm text-ink-muted">{@render sub()}</p>
			{/if}
		</div>
		{#if action}
			<div class="shrink-0">{@render action()}</div>
		{/if}
	</div>
</div>
