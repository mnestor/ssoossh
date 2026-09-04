<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { resolveConsoleCode } from '$lib/api/endpoints';
	import { redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Card from '$lib/components/Card.svelte';
	import { describeCodeError, formatCode, normalizeCode, type CodeFailure } from '$lib/consolecode';

	// The short form of the verification URL: /c/<code>, the equivalent of
	// RFC 8628's verification_uri_complete. A console prints it alongside
	// the code, and where the terminal can draw one, as a QR code — this is
	// the address that has to fit in an 80-column QR, which is why it is
	// this short and not /approve/<uuid>.
	//
	// It is a shortcut through the UI, not around the authentication: the
	// resolve call behind it needs a session exactly as the code box does,
	// and a signed-out visitor is sent to /login and lands back here.

	const code = $derived(normalizeCode(page.params.code ?? ''));

	let failure = $state<CodeFailure | null>(null);

	$effect(() => {
		const submitted = code;
		if (!submitted) {
			failure = describeCodeError(new Error('That link carries no code.'));
			return;
		}

		let cancelled = false;
		failure = null;

		resolveConsoleCode(submitted)
			.then((resolved) => {
				if (cancelled) {
					return;
				}
				// Client-side, for the reason /console's submit handler
				// spells out: a document GET would hand the browser-level
				// claim to whoever loads the page first, and the machine
				// that started the login has held its request id all along.
				// eslint-disable-next-line svelte/no-navigation-without-resolve
				return goto(resolved.approval_url);
			})
			.catch((cause) => {
				if (cancelled || redirectIfUnauthenticated(cause)) {
					return;
				}
				failure = describeCodeError(cause);
			});

		return () => {
			cancelled = true;
		};
	});
</script>

<svelte:head><title>Console login · ssoossh</title></svelte:head>

<div class="w-full max-w-[560px]">
	{#if failure}
		<Card title={failure.title} testid="console-link-failure-{failure.kind}">
			<p class="text-sm text-ink-muted">{failure.message}</p>
			<p class="mt-3 text-sm text-ink-muted">
				<!-- Resolved route id, so a rename of /console fails the build
				     rather than 404ing here. -->
				<a class="text-accent underline" href={resolve('/console')}>Type the code instead</a>
			</p>
		</Card>
	{:else}
		<Alert testid="console-link-resolving">
			Checking code {formatCode(code)}…
		</Alert>
	{/if}
</div>
