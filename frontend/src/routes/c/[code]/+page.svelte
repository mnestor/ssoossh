<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { resolveConsoleCode } from '#lib/api/endpoints.js';
	import { currentPath, goToLogin, redirectIfUnauthenticated } from '#lib/auth.js';
	import { getBranding } from '#lib/branding.svelte.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageShell from '#lib/components/PageShell.svelte';
	import { session } from '#lib/session.svelte.js';
	import {
		describeCodeError,
		formatCode,
		normalizeCode,
		type CodeFailure
	} from '#lib/consolecode.js';

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

	// A link can be sent to anyone; a code on a screen cannot. So the link
	// shows its code and asks before it claims anything, which is what
	// RFC 8628 asks of a verification_uri_complete (§3.3.1, §5.4): resolving
	// on load took whoever clicked a forwarded link straight to "Approve a
	// console login" with nothing asking whether it was their screen.
	let confirmed = $state(false);
	let declined = $state(false);

	// Signed out, sign in first, as /console does: the question below should
	// be answered by the account that will approve, and a code that dies in
	// about a minute and a half should not wait behind it twice.
	$effect(() => {
		if (session.resolved && !session.signedIn && !session.error) {
			goToLogin(currentPath());
		}
	});

	const branding = $derived(getBranding());
	const supportEmail = $derived(branding.support_email ?? '');
	const supportLabel = $derived(branding.support_label || supportEmail);

	// Retyping helps only where the code itself may be the problem. An
	// expired or claimed login fails the same way however it is entered.
	const offerTyping = $derived(
		failure !== null &&
			(failure.kind === 'not-found' || failure.kind === 'invalid' || failure.kind === 'unknown')
	);

	$effect(() => {
		const submitted = code;
		if (!submitted) {
			failure = describeCodeError(new Error('That link carries no code.'));
			return;
		}
		if (!confirmed) {
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

<PageShell width="focus">
	{#if failure}
		<!-- Bound once: a snippet is its own closure, so the `failure` the
		     branch narrowed does not reach inside one. -->
		{@const loadFailure = failure}
		<div data-testid="console-link-failure-{failure.kind}" class="rounded-lg bg-surface p-5">
			<PageHeading title={loadFailure.title}>
				{#snippet sub()}{loadFailure.message}{/snippet}
			</PageHeading>
			{#if offerTyping}
				<p class="mt-3 text-sm">
					<!-- Resolved route id, so a rename of /console fails the build
					     rather than 404ing here. -->
					<a
						class="inline-flex min-h-target items-center text-accent underline"
						href={resolve('console')}
						data-testid="console-link-type-instead">Type the code instead</a
					>
				</p>
			{/if}
		</div>
	{:else if declined}
		<div data-testid="console-link-declined" class="rounded-lg bg-surface p-5">
			<PageHeading title="Nothing was approved">
				{#snippet sub()}
					The code on the link is not the one in front of you, so this is a login you did not start.
				{/snippet}
			</PageHeading>
			<p class="mt-4 text-sm text-ink">
				If someone sent you this link, don't use it or ask for another.
				{#if supportEmail}
					<!-- A mailto:, not a route: resolve() has nothing to check a
					     mail client handoff against. -->
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
					Tell
					<a class="font-medium text-accent hover:underline" href="mailto:{supportEmail}"
						>{supportLabel}</a
					>.
				{:else}
					Tell your administrator.
				{/if}
			</p>
			<p class="mt-3 text-sm">
				<a
					class="inline-flex min-h-target items-center font-medium text-accent hover:underline"
					href={resolve('dashboard')}>Go to your dashboard</a
				>
			</p>
		</div>
	{:else if !confirmed}
		<div data-testid="console-link-confirm" class="flex flex-col gap-5 rounded-lg bg-surface p-5">
			<PageHeading title="Is this the code on the screen in front of you?" />
			<p
				class="text-center font-mono text-3xl tracking-[0.2em] text-ink"
				data-testid="console-link-code"
			>
				{formatCode(code)}
			</p>
			<p class="text-sm text-ink-muted">
				Only continue if you just started a login at a machine's console and it shows this code. If
				someone sent you this link, don't continue.
			</p>
			<div class="flex flex-col-reverse gap-2 sm:flex-row sm:justify-end">
				<Button variant="ghost" onclick={() => (declined = true)} testid="console-link-no">
					No, it doesn't match
				</Button>
				<Button onclick={() => (confirmed = true)} testid="console-link-yes">Yes, continue</Button>
			</div>
		</div>
	{:else}
		<Alert testid="console-link-resolving">
			Checking code {formatCode(code)}…
		</Alert>
	{/if}
</PageShell>
