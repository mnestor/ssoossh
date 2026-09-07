<script lang="ts">
	import type { EnrollmentRetrievalResponse } from '$lib/api/generated/webtypes';
	import { formatDateTime } from '$lib/format';
	import MonoChip from './MonoChip.svelte';
	import PageSection from './PageSection.svelte';

	// Every redemption of one code: when, from where, and whether a
	// certificate actually came out. Shared by both service-code pages, and
	// last on both — it is the one part of the page that grows without bound,
	// and the facts and controls above it are what a reader came for.
	//
	// It answers the question the summary rows above it used to only gesture
	// at. "Last redeemed, 12 redemptions" says a job is alive; which host
	// pulled the certificate, and whether any attempt failed, is what tells
	// an operator whether it is the job they think it is.
	interface Props {
		/** The most recent page of the log, newest first. */
		retrievals: EnrollmentRetrievalResponse[];
		/** Every logged redemption, not just the ones in `retrievals`. */
		total: number;
	}

	let { retrievals, total }: Props = $props();

	// The server caps the log it returns, so the page has to say what it is
	// showing a slice of rather than let the last row read as the first
	// redemption.
	const truncated = $derived(retrievals.length < total);
</script>

<PageSection title="Redemption history">
	{#if retrievals.length === 0}
		<p class="text-[13px] text-ink-muted">Never redeemed.</p>
	{:else}
		{#if truncated}
			<!-- Said before the list, not after it: a reader who stops scrolling
			     partway through still needs to know this is the recent end of a
			     longer history, not all of it. -->
			<p class="mb-2 text-[13px] text-ink-muted">
				The {retrievals.length} most recent of {total} redemptions.
			</p>
		{/if}
		<dl class="divide-y divide-border-subtle">
			<!-- Keyed by position: reusable codes mean two redemptions can land
			     in the same second, and a keyed each throws on a duplicate key
			     rather than rendering it. -->
			{#each retrievals as retrieval, index (index)}
				<div class="flex items-center justify-between gap-3 py-3">
					<div>
						<div class="text-[13px]">{formatDateTime(retrieval.retrieved_at)}</div>
						<div class="mt-1 flex items-center gap-1.5">
							<MonoChip>{retrieval.source_ip}</MonoChip>
							{#if !retrieval.succeeded}
								<span class="text-[11px] font-semibold text-danger">Failed</span>
							{/if}
						</div>
					</div>
					<span class="text-[11px] text-ink-muted">
						Serial <span class="font-mono">{retrieval.certificate_serial}</span>
					</span>
				</div>
			{/each}
		</dl>
	{/if}
</PageSection>
