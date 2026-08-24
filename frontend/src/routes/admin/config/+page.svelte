<script lang="ts">
	import { onMount } from 'svelte';
	import { getAdminConfig } from '$lib/api/endpoints';
	import type { EffectiveConfigResponse } from '$lib/api/types';

	let config: EffectiveConfigResponse | null = $state(null);
	let error: string | null = $state(null);
	let busy = $state(false);

	async function loadConfig() {
		busy = true;
		error = null;
		try {
			config = await getAdminConfig();
		} catch (cause) {
			error = cause instanceof Error ? cause.message : 'Failed to load configuration';
		} finally {
			busy = false;
		}
	}

	onMount(loadConfig);
</script>

<div class="flex max-w-full flex-col gap-6">
	<div>
		<h1 class="text-2xl font-bold text-ink">Server Configuration</h1>
		<p class="text-sm text-ink-muted">Effective configuration (sensitive fields redacted)</p>
	</div>

	{#if busy}
		<div class="text-center text-ink-muted">Loading...</div>
	{:else if error}
		<div class="rounded-lg border border-danger-surface bg-danger-surface p-4 text-sm text-danger">
			{error}
		</div>
	{:else if config}
		<!-- HTTP Settings -->
		<div class="rounded-lg border border-border-subtle p-4">
			<h2 class="mb-3 font-semibold text-ink">HTTP</h2>
			<div class="grid gap-4 text-sm sm:grid-cols-2">
				<div>
					<p class="font-mono text-xs text-ink-muted">server_name</p>
					<p>{config.server_name}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">port</p>
					<p>{config.port}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">is_https</p>
					<p>{config.is_https ? 'true' : 'false'}</p>
				</div>
			</div>
		</div>

		<!-- Database -->
		<div class="rounded-lg border border-border-subtle p-4">
			<h2 class="mb-3 font-semibold text-ink">Database</h2>
			<div class="grid gap-4 text-sm sm:grid-cols-2">
				<div>
					<p class="font-mono text-xs text-ink-muted">provider</p>
					<p>{config.db_provider}</p>
				</div>
			</div>
		</div>

		<!-- OIDC -->
		<div class="rounded-lg border border-border-subtle p-4">
			<h2 class="mb-3 font-semibold text-ink">OIDC Authentication</h2>
			<div class="grid gap-4 text-sm sm:grid-cols-2">
				<div>
					<p class="font-mono text-xs text-ink-muted">provider_url</p>
					<p class="truncate">{config.provider_url}</p>
				</div>
			</div>
		</div>

		<!-- Admin Authorization -->
		<div class="rounded-lg border border-border-subtle p-4">
			<h2 class="mb-3 font-semibold text-ink">Admin Authorization</h2>
			<div class="grid gap-4 text-sm sm:grid-cols-2">
				<div>
					<p class="font-mono text-xs text-ink-muted">admin_require_group</p>
					<p>{config.admin_require_group || '(not configured)'}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">admin_auditor_group</p>
					<p>{config.admin_auditor_group || '(not configured)'}</p>
				</div>
			</div>
		</div>

		<!-- Logging -->
		<div class="rounded-lg border border-border-subtle p-4">
			<h2 class="mb-3 font-semibold text-ink">Logging</h2>
			<div class="grid gap-4 text-sm sm:grid-cols-2">
				<div>
					<p class="font-mono text-xs text-ink-muted">level</p>
					<p>{config.logging_level}</p>
				</div>
			</div>
		</div>

		<!-- Certificate Options -->
		<div class="rounded-lg border border-border-subtle p-4">
			<h2 class="mb-3 font-semibold text-ink">Certificate Options</h2>
			<div class="grid gap-4 text-sm sm:grid-cols-3">
				<div>
					<p class="font-mono text-xs text-ink-muted">client_timeout</p>
					<p>{config.cert_client_timeout}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">approval_ttl</p>
					<p>{config.cert_approval_ttl}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">signing_grace</p>
					<p>{config.cert_signing_grace}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">user_valid_duration</p>
					<p>{config.cert_user_valid_duration}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">service_valid_duration</p>
					<p>{config.cert_service_valid_duration}</p>
				</div>
				<div>
					<p class="font-mono text-xs text-ink-muted">pam_valid_duration</p>
					<p>{config.cert_pam_valid_duration}</p>
				</div>
			</div>
		</div>
	{/if}
</div>
