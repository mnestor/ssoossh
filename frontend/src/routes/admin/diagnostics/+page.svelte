<script lang="ts">
	import { runDiagnostics } from '#lib/api/endpoints.js';
	import type { DiagnosticCheckResult, DiagnosticsResponse } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import ListStatus from '#lib/components/ListStatus.svelte';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import MonoChip from '#lib/components/MonoChip.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import SectionLabel from '#lib/components/SectionLabel.svelte';

	// Admin-only: diagnostics/+layout.svelte turns SOC and auditors away
	// before this page renders, so it has no non-admin branch.

	let running = $state(false);
	let report = $state<DiagnosticsResponse | null>(null);
	let error = $state<string | null>(null);

	async function run() {
		running = true;
		error = null;
		try {
			report = await runDiagnostics();
		} catch (cause) {
			// An expired session is a sign-in, not a failed check.
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			error = errorMessage(cause);
		} finally {
			running = false;
		}
	}

	// The badge colour and label for a check's status. Skipped is deliberately
	// neutral, not a pass: a check that could not run is not an all-clear.
	//
	// The same four pairs StatusBadge uses, from the same tokens. These were
	// raw Tailwind palette literals with a hand-written `dark:` override on
	// each one, which is the only place in the app that carried a colour the
	// theme did not know about: the light values came from a different green
	// and a different red than the rest of the app, and the dark values had
	// to be maintained by hand because a literal cannot follow a token swap.
	const badges: Record<string, { label: string; class: string }> = {
		ok: { label: 'OK', class: 'bg-granted-surface text-granted' },
		warn: { label: 'Warning', class: 'bg-trimmed-surface text-trimmed' },
		critical: { label: 'Critical', class: 'bg-danger-surface text-danger' },
		skipped: { label: 'Skipped', class: 'bg-surface-muted text-ink-muted' }
	};

	function badge(status: string): { label: string; class: string } {
		return badges[status] ?? badges.skipped;
	}

	// A check needs the operator's eye when it is anything but a clean pass.
	function needsAttention(c: DiagnosticCheckResult): boolean {
		return c.status !== 'ok';
	}

	// Worst first, so a critical check is never below several OKs.
	const severityOrder: Record<string, number> = { critical: 0, warn: 1, ok: 2, skipped: 3 };
	const orderedChecks = $derived(
		report
			? [...report.checks].sort(
					(a, b) => (severityOrder[a.status] ?? 3) - (severityOrder[b.status] ?? 3)
				)
			: []
	);

	// One line before the detail: how many checks ran and how many need a
	// look. Also the announcement, so pressing Run checks is answered for a
	// screen reader too (WCAG 4.1.3).
	const summary = $derived.by(() => {
		if (!report) {
			return '';
		}
		const count = (status: string) => report!.checks.filter((c) => c.status === status).length;
		const critical = count('critical');
		const warn = count('warn');
		const parts = [`${report.checks.length} ${report.checks.length === 1 ? 'check' : 'checks'}`];
		parts.push(critical ? `${critical} critical` : 'none critical');
		if (warn) {
			parts.push(`${warn} ${warn === 1 ? 'warning' : 'warnings'}`);
		}
		return parts.join(', ') + '.';
	});
</script>

<svelte:head><title>Diagnostics · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Diagnostics">
		{#snippet action()}
			<Button onclick={run} busy={running} testid="run-diagnostics">
				{running ? 'Running…' : 'Run checks'}
			</Button>
		{/snippet}
	</PageHeading>

	<p class="max-w-2xl text-sm text-ink-muted">
		Runs a set of read-only checks against this deployment: whether the server can reach its own
		public URL, how the client IP is resolved through
		<MonoChip>http.trusted_proxies</MonoChip>, whether the edge in front of the app weakens the
		security headers it sets, whether the edge adds dangerous CORS headers, and how the identity
		provider's UserInfo endpoint is doing (<MonoChip>authentication.userinfo</MonoChip>): healthy,
		failing, or paused with the time of the next retry. The edge checks call
		<MonoChip>http.public_url</MonoChip> and nothing else; nothing is written.
	</p>

	{#if error}
		<Alert variant="error" title="The checks could not run" onretry={run} testid="diagnostics-error"
			>{error}</Alert
		>
	{/if}

	<ListStatus message={summary} />

	{#if report}
		<p class="text-sm font-medium text-ink" data-testid="diagnostics-summary">{summary}</p>
		{#if report.public_origin}
			<p class="text-sm text-ink-muted">
				Probed <MonoChip>{report.public_origin}</MonoChip>.
			</p>
		{/if}

		<!-- One column of checks separated by a rule, rather than boxes
		     two abreast. A run is a report read in the order the server
		     ran it: a masonry grid reordered it by height, and a reader
		     comparing this run to the last one had to find each check
		     again rather than read down the same list. -->
		<div data-testid="diagnostics-results" class="flex flex-col gap-8">
			{#each orderedChecks as check (check.id)}
				<section class="rounded-lg bg-surface p-5">
					<div class="flex items-start justify-between gap-4">
						<div>
							<h2 class="text-base font-semibold text-ink">{check.title}</h2>
							<p class="mt-1 text-sm text-ink-muted">{check.summary}</p>
						</div>
						<span
							class="rounded px-2.5 py-1 text-xs font-semibold whitespace-nowrap {badge(
								check.status
							).class}"
						>
							{badge(check.status).label}
						</span>
					</div>

					{#if check.findings.length > 0}
						<ul class="mt-3 flex list-disc flex-col gap-1.5 pl-5 text-sm text-ink">
							{#each check.findings as finding (finding)}
								<li>{finding}</li>
							{/each}
						</ul>
					{/if}

					{#if check.remediation && needsAttention(check)}
						<div class="mt-3">
							<SectionLabel>How to fix</SectionLabel>
							<p class="text-sm text-ink">{check.remediation}</p>
						</div>
					{/if}
				</section>
			{/each}
		</div>
	{:else if !error}
		<p class="text-sm text-ink-muted">
			Press <strong>Run checks</strong> to test this deployment.
		</p>
	{/if}
</PageShell>
