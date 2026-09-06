<script lang="ts">
	import { runDiagnostics } from '$lib/api/endpoints';
	import type { DiagnosticCheckResult, DiagnosticsResponse } from '$lib/api/types';
	import { errorMessage } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import MonoChip from '$lib/components/MonoChip.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import { session } from '$lib/session.svelte';

	const isAdmin = $derived(session.user?.is_admin ?? false);

	let running = $state(false);
	let report = $state<DiagnosticsResponse | null>(null);
	let error = $state<string | null>(null);

	async function run() {
		running = true;
		error = null;
		try {
			report = await runDiagnostics();
		} catch (cause) {
			error = errorMessage(cause);
		} finally {
			running = false;
		}
	}

	// The badge colour and label for a check's status. Skipped is deliberately
	// neutral, not a pass: a check that could not run is not an all-clear.
	const badges: Record<string, { label: string; class: string }> = {
		ok: { label: 'OK', class: 'bg-green-500/15 text-green-700 dark:text-green-400' },
		warn: { label: 'Warning', class: 'bg-amber-500/15 text-amber-700 dark:text-amber-400' },
		critical: { label: 'Critical', class: 'bg-red-500/15 text-red-700 dark:text-red-400' },
		skipped: { label: 'Skipped', class: 'bg-ink-muted/15 text-ink-muted' }
	};

	function badge(status: string): { label: string; class: string } {
		return badges[status] ?? badges.skipped;
	}

	// A check needs the operator's eye when it is anything but a clean pass.
	function needsAttention(c: DiagnosticCheckResult): boolean {
		return c.status !== 'ok';
	}
</script>

<div class="flex flex-col gap-6">
	<PageHeading eyebrow="Admin" title="Diagnostics">
		{#snippet action()}
			{#if isAdmin}
				<Button onclick={run} busy={running} testid="run-diagnostics">
					{running ? 'Running...' : 'Run checks'}
				</Button>
			{/if}
		{/snippet}
	</PageHeading>

	{#if !isAdmin}
		<Alert variant="warning" title="Admin only">
			These deployment self-checks are available to administrators only.
		</Alert>
	{:else}
		<p class="max-w-2xl text-sm text-ink-muted">
			Runs a set of read-only checks against this deployment: whether the server can reach its own
			public URL, how the client IP is resolved through
			<MonoChip>http.trusted_proxies</MonoChip>, whether the edge in front of the app weakens the
			security headers it sets, and whether the edge adds dangerous CORS headers. The edge checks
			call
			<MonoChip>http.public_url</MonoChip> and nothing else; nothing is written.
		</p>

		{#if error}
			<Alert variant="error" title="The checks could not run">{error}</Alert>
		{/if}

		{#if report}
			{#if report.public_origin}
				<p class="text-sm text-ink-muted">
					Probed <MonoChip>{report.public_origin}</MonoChip>.
				</p>
			{/if}

			<div class="flex flex-col gap-4" data-testid="diagnostics-results">
				{#each report.checks as check (check.id)}
					<Card>
						<div class="flex items-start justify-between gap-4">
							<div>
								<h2 class="text-base font-semibold text-ink">{check.title}</h2>
								<p class="mt-1 text-sm text-ink-muted">{check.summary}</p>
							</div>
							<span
								class="rounded-full px-2.5 py-1 text-xs font-semibold whitespace-nowrap {badge(
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
							<div class="mt-3 rounded-md border border-border-subtle bg-surface-muted px-3 py-2">
								<div class="mb-1 text-xs font-semibold tracking-wide text-ink-muted uppercase">
									How to fix
								</div>
								<p class="text-sm text-ink">{check.remediation}</p>
							</div>
						{/if}
					</Card>
				{/each}
			</div>
		{:else if !error}
			<p class="text-sm text-ink-muted">
				Press <strong>Run checks</strong> to test this deployment.
			</p>
		{/if}
	{/if}
</div>
