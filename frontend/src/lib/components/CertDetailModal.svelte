<script lang="ts">
	import type { CertificateRecord } from '$lib/api/types';
	import { formatDateTime } from '$lib/format';
	import Card from './Card.svelte';
	import DetailRow from './DetailRow.svelte';
	import Icon from './Icon.svelte';

	interface Props {
		cert: CertificateRecord;
		onclosed: () => void;
	}

	let { cert, onclosed }: Props = $props();
	let dialogEl = $state<HTMLDialogElement | undefined>(undefined);

	// Call showModal() to get native modal behavior (top-layer stacking,
	// backdrop, focus trap) and prevent closing with Escape.
	$effect(() => {
		dialogEl?.showModal();
	});

	function handleClose() {
		dialogEl?.close();
		onclosed();
	}

	// Icon mapping for certificate types
	const certTypeIcons: Record<string, string> = {
		user: 'user',
		pam: 'terminal',
		service: 'cog',
		host: 'server'
	};

	// Split principals string on comma and trim whitespace
	const principals = $derived(
		cert.principals
			.split(',')
			.map((p) => p.trim())
			.filter((p) => p.length > 0)
	);
</script>

<dialog bind:this={dialogEl} class="fixed inset-0 z-50 flex items-center justify-center p-4 backdrop-blur-sm">
	<div class="max-h-[90vh] w-full max-w-2xl overflow-y-auto">
		<Card title="Certificate Details" description="Issued certificate audit record">
			{#snippet footer()}
				<div class="flex gap-3">
					<button
						onclick={handleClose}
						class="rounded-md border border-border-subtle px-4 py-2 text-sm font-medium transition hover:bg-surface-muted"
					>
						Close
					</button>
				</div>
			{/snippet}

			<dl class="divide-y divide-border-subtle">
				<DetailRow label="Certificate type">
					<div class="inline-flex items-center justify-center rounded bg-surface-muted px-2 py-1.5">
						<Icon
							name={certTypeIcons[cert.type] || 'zap'}
							size="md"
							ariaLabel="Certificate type: {cert.type}"
						/>
					</div>
				</DetailRow>

				{#if principals.length > 0}
					<DetailRow label="Principals">
						<div class="flex flex-wrap gap-2">
							{#each principals as principal (principal)}
								<code
									class="rounded border border-border-subtle bg-surface px-2 py-0.5 font-mono text-xs text-ink"
								>
									{principal}
								</code>
							{/each}
						</div>
					</DetailRow>
				{/if}

				<DetailRow label="Serial number" mono>{cert.serial_number}</DetailRow>
				<DetailRow label="Key fingerprint" mono>{cert.public_key_fingerprint}</DetailRow>

				{#if cert.hostname}
					<DetailRow label="Hostname">{cert.hostname}</DetailRow>
				{/if}

				<DetailRow label="Issued at">{formatDateTime(cert.issued_at)}</DetailRow>
				<DetailRow label="Expires at">{formatDateTime(cert.expires_at)}</DetailRow>
				<DetailRow label="Key ID" mono>{cert.key_id}</DetailRow>
			</dl>
		</Card>
	</div>
</dialog>

<style>
	dialog {
		border: none;
		padding: 0;
		background: transparent;
		::backdrop {
			background-color: rgba(0, 0, 0, 0.5);
		}
	}
</style>
