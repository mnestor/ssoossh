<script lang="ts">
	import { ApiError } from '#lib/api/client.js';
	import { redeemIdentityEcho } from '#lib/api/endpoints.js';
	import type { IdentityEchoPayload } from '#lib/api/types.js';
	import { errorMessage } from '#lib/auth.js';
	import { readEchoFragment, readFailureDetail } from '#lib/identityEcho.js';
	import { describeSignInFailure, type SignInFailure } from '#lib/signin.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import EchoResult from '#lib/components/EchoResult.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	// The bootstrap claims echo: the page a `ssoosshd identity echo` link
	// opens. It is for first setup, when no sign-in works yet and no admin
	// exists, so it needs no session and shows no navigation. The link's
	// one-time code and, later, the result both arrive in the fragment,
	// which no server and no log sees; each is read once and dropped from
	// the address bar and from history.

	type View =
		| { kind: 'empty' }
		| { kind: 'code'; code: string }
		| { kind: 'result'; payload: IdentityEchoPayload }
		| { kind: 'failed'; failure: SignInFailure; detail: string }
		| { kind: 'unreadable' };

	let view = $state<View>({ kind: 'empty' });
	let redeeming = $state(false);
	let redeemError = $state<{ title: string; message: string } | null>(null);

	$effect(() => {
		const failed = new URLSearchParams(window.location.search).get('error');
		const hash = window.location.hash;
		if (failed || hash) {
			history.replaceState(null, '', window.location.pathname);
		}
		const failure = describeSignInFailure(failed);
		if (failure) {
			view = { kind: 'failed', failure, detail: readFailureDetail(hash) };
			return;
		}
		const fragment = readEchoFragment(hash);
		switch (fragment.kind) {
			case 'code':
				view = { kind: 'code', code: fragment.code };
				break;
			case 'payload':
				view = { kind: 'result', payload: fragment.payload };
				break;
			case 'unreadable':
				view = { kind: 'unreadable' };
				break;
		}
	});

	// Redeeming is a press, not a page load: the code works once, and the
	// reader should know it is being spent and where they are about to go.
	async function redeem(code: string) {
		redeeming = true;
		redeemError = null;
		try {
			const { authorization_url } = await redeemIdentityEcho(code);
			window.location.href = authorization_url;
		} catch (cause) {
			redeeming = false;
			redeemError =
				cause instanceof ApiError && cause.isNotFound
					? {
							title: 'This link does not work',
							message:
								'It is wrong, already used, or expired. Run ssoosshd identity echo on the server host again for a new one.'
						}
					: { title: 'Could not start the sign-in', message: errorMessage(cause) };
		}
	}
</script>

<svelte:head><title>Identity echo · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Identity echo">
		{#snippet sub()}
			What your identity provider sends, checked against this server's configuration.
		{/snippet}
	</PageHeading>

	{#if view.kind === 'code'}
		{@const code = view.code}
		<PageSection
			title="Sign in to see your claims"
			description="A one-time link from ssoosshd identity echo. It works once."
			testid="echo-redeem"
		>
			<div class="flex max-w-[70ch] flex-col gap-4 text-sm">
				<p class="text-ink-muted">
					Signing in shows you the claims your identity provider sends for your account: which
					configured field reads each one, whether a login would succeed, and configuration to copy.
					It shows only your own claims, creates no session and no account, and stores nothing.
				</p>
				<div>
					<Button testid="echo-redeem-start" busy={redeeming} onclick={() => redeem(code)}>
						{redeeming ? 'Redirecting…' : 'Sign in to see my claims'}
					</Button>
				</div>
				{#if redeemError}
					<Alert variant="error" title={redeemError.title} testid="echo-redeem-error">
						{redeemError.message}
					</Alert>
				{/if}
			</div>
		</PageSection>
	{:else if view.kind === 'result'}
		<EchoResult payload={view.payload} />
	{:else if view.kind === 'failed'}
		<Alert variant="error" title={view.failure.title} testid="echo-failure">
			{view.failure.message}
			{#if view.detail}
				<span
					class="mt-2 block font-mono text-dense wrap-anywhere"
					data-testid="echo-failure-detail">{view.detail}</span
				>
			{/if}
			<span class="mt-2 block">
				The link has been used. Run <code>ssoosshd identity echo</code> again for a new one; the command
				that printed it shows this error too.
			</span>
		</Alert>
	{:else if view.kind === 'unreadable'}
		<Alert variant="error" title="Could not read this link" testid="echo-read-error">
			The address is incomplete. Copy the whole link that ssoosshd identity echo printed, or run it
			again.
		</Alert>
	{:else}
		<PageSection title="Open a link to start" testid="echo-empty">
			<p class="max-w-[70ch] text-sm text-ink-muted">
				This page opens from a one-time link. Run <code>ssoosshd identity echo</code> on the server host
				as the service account, open the link it prints, and sign in.
			</p>
		</PageSection>
	{/if}
</PageShell>
