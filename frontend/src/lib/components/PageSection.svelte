<script lang="ts">
	import type { Snippet } from 'svelte';

	// One section of a page: a subtle card — a hairline border and a tinted
	// ground, no shadow — holding a quiet uppercase heading, an optional
	// line of copy, and the fields themselves.
	//
	// This is what replaced Card. Card was a white panel with a shadow and a
	// ruled-off header, which read as a component floating over the page;
	// this reads as a region of it. The frame is still doing work — it is
	// what says where one group of fields ends and the next begins, which on
	// a page of six of them is the difference between a document and a list
	// — but it says it once, quietly, instead of stacking a border, a tint,
	// a shadow and a divider to say it four times.
	//
	// A real h2 rather than a styled div: a section is a landmark a screen
	// reader navigates by, and it was Card's header that carried that, not
	// its border. SectionLabel stays for the labels *inside* a section,
	// which are not headings and must not become them.
	//
	// One level only. Nothing inside one of these gets a frame of its own:
	// a card inside a card says the inner thing is separate from the page,
	// which is the one thing it is not.
	interface Props {
		/** The section's heading. */
		title: string;
		/** An optional line under it, for what the fields themselves do not say. */
		description?: string;
		/** Stable selector for the e2e browser tier — see test/e2e/README.md. */
		testid?: string;
		children: Snippet;
	}

	let { title, description, testid, children }: Props = $props();
</script>

<section data-testid={testid} class="rounded-lg border border-border-subtle bg-surface-muted p-4">
	<h2 class="text-[11px] font-semibold tracking-[0.06em] text-ink-muted uppercase">
		{title}
	</h2>
	{#if description}
		<p class="mt-1 text-[13px] text-ink-muted">{description}</p>
	{/if}
	<div class="mt-3">
		{@render children()}
	</div>
</section>
