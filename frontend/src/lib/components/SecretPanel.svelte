<script lang="ts">
	import { formatDateTime } from '#lib/format.js';
	import { nssKeyFile } from '#lib/nss.js';
	import Alert from './Alert.svelte';
	import Button from './Button.svelte';
	import Icon from './Icon.svelte';
	import PageSection from './PageSection.svelte';

	// A fleet key's secret, the one time the server sends it: after a create
	// and after a rotation. Losing it means rotating again and touching every
	// host a second time, so the panel is built around storing it:
	//
	// - the two-line nss.key file is the main thing on it, ready to paste,
	//   because that is what a host actually reads;
	// - every value has its own copy button, and a refused clipboard says so
	//   and leaves the value selectable;
	// - Done stays disabled until "I have stored this secret" is ticked, with
	//   the reason beside it, so the panel cannot be waved away by reflex.
	//
	// The secret is a prop and nothing here copies it into state: the caller
	// drops its own reference in ondismiss, and once this unmounts the page
	// holds the secret nowhere.
	interface Props {
		keyId: string;
		secret: string;
		/** Set after a rotation: when the previous secret stops being accepted. */
		previousExpiresAt?: string;
		/** Called by Done, once the reader has ticked the acknowledgement. */
		ondismiss: () => void;
		testid?: string;
	}

	let { keyId, secret, previousExpiresAt, ondismiss, testid = 'secret-panel' }: Props = $props();

	let stored = $state(false);
	// Per value: which one last landed on the clipboard, and which one the
	// browser refused.
	let copied = $state<string | null>(null);
	let failed = $state<string | null>(null);

	const file = $derived(nssKeyFile(keyId, secret));

	const values = $derived([
		{ id: 'key-id', label: 'Key id', value: keyId },
		{ id: 'secret', label: 'Secret', value: secret }
	]);

	async function copy(id: string, value: string) {
		try {
			await navigator.clipboard.writeText(value);
			copied = id;
			failed = null;
		} catch {
			copied = null;
			failed = id;
		}
	}
</script>

{#snippet copyButton(id: string, label: string, value: string)}
	<button
		type="button"
		onclick={() => copy(id, value)}
		data-testid="{testid}-copy-{id}"
		class="inline-flex min-h-target shrink-0 items-center gap-1.5 rounded-md border border-border-control px-3 text-xs font-medium text-ink transition hover:bg-surface-muted"
	>
		<Icon name={copied === id ? 'copy-check' : 'copy'} size="xs" />
		{copied === id ? 'Copied' : `Copy ${label.toLowerCase()}`}
	</button>
{/snippet}

<PageSection
	title="Secret"
	description="Shown this once. The server keeps only a hash, so nobody can read it back later."
	{testid}
>
	<div class="flex max-w-[70ch] flex-col gap-5">
		<Alert variant="warning" title="Store the secret now" testid="{testid}-warning">
			It cannot be shown again. If it is lost, rotate the key and update every host.
		</Alert>

		<div class="flex flex-col gap-2">
			<div class="flex flex-wrap items-center justify-between gap-2">
				<span class="text-sm font-medium text-ink">nss.key</span>
				{@render copyButton('file', 'File', file)}
			</div>
			<pre
				class="overflow-x-auto rounded-md bg-surface-muted px-3 py-2 font-mono text-dense break-all whitespace-pre-wrap text-ink select-all"
				data-testid="{testid}-file">{file}</pre>
			<p class="text-dense text-ink-muted">
				Save it on each host as <code class="font-mono">/etc/ssoossh/nss.key</code>, owned by root
				with mode 0600: line 1 the key id, line 2 the secret.
			</p>
		</div>

		<div class="flex flex-col gap-1">
			<dl class="flex flex-col divide-y divide-border-subtle">
				{#each values as item (item.id)}
					<div class="flex flex-wrap items-center gap-x-3 gap-y-1 py-2">
						<dt class="w-20 shrink-0 text-dense text-ink-muted">{item.label}</dt>
						<dd class="flex min-w-0 flex-1 flex-wrap items-center justify-between gap-2">
							<span
								class="min-w-0 font-mono text-dense break-all text-ink select-all"
								data-testid="{testid}-{item.id}">{item.value}</span
							>
							{@render copyButton(item.id, item.label, item.value)}
						</dd>
					</div>
				{/each}
			</dl>

			<!-- Always in the document, so the live region exists before it
			     speaks. -->
			<p role="status" class="text-xs text-danger" data-testid="{testid}-copy-failed">
				{#if failed}
					Copy failed. Select the value above and copy it by hand.
				{/if}
			</p>
		</div>

		{#if previousExpiresAt}
			<p class="text-dense text-ink" data-testid="{testid}-grace">
				The previous secret is accepted until {formatDateTime(previousExpiresAt)}. Hosts still
				presenting it after that stop resolving accounts.
			</p>
		{/if}

		<div class="flex flex-col gap-3 border-t border-border-subtle pt-4">
			<label class="flex min-h-target items-center gap-2 text-sm text-ink">
				<input type="checkbox" bind:checked={stored} data-testid="{testid}-stored" />
				I have stored this secret
			</label>
			<div class="flex flex-wrap items-center gap-3">
				<Button disabled={!stored} onclick={ondismiss} testid="{testid}-done">Done</Button>
				{#if !stored}
					<span class="text-dense text-ink-muted" data-testid="{testid}-done-reason">
						Tick the box once the secret is stored.
					</span>
				{/if}
			</div>
		</div>
	</div>
</PageSection>
