<script lang="ts">
	import { startIdentityEcho } from '#lib/api/endpoints.js';
	import type { IdentityEchoPayload } from '#lib/api/types.js';
	import { errorMessage, redirectIfUnauthenticated } from '#lib/auth.js';
	import { readEchoFragment, readFailureDetail } from '#lib/identityEcho.js';
	import { describeSignInFailure, type SignInFailure } from '#lib/signin.js';
	import Alert from '#lib/components/Alert.svelte';
	import Button from '#lib/components/Button.svelte';
	import EchoResult from '#lib/components/EchoResult.svelte';
	import PageHeading from '#lib/components/PageHeading.svelte';
	import PageSection from '#lib/components/PageSection.svelte';
	import PageShell from '#lib/components/PageShell.svelte';

	let payload = $state<IdentityEchoPayload | null>(null);
	let readError = $state<string | null>(null);
	let starting = $state(false);
	let startError = $state<string | null>(null);
	// Why the last echo did not complete, when the callback sent the
	// browser back here with ?error= instead of a result, and the error
	// itself when the callback put one in the fragment.
	let echoFailure = $state<SignInFailure | null>(null);
	let failureDetail = $state('');

	// Starting an echo is admin-only on the server (controller/identityecho.go),
	// and identity/echo/+layout.svelte turns SOC and auditors away before
	// this page renders, so it has no non-admin branch.

	// The result arrives in the URL fragment, which is never sent to a
	// server. Read it once, then drop it from the address bar and from
	// history, so the claims live on this page and nowhere else: not in a
	// database, not in the session, not in an access log, and not in a URL
	// somebody screenshots.
	$effect(() => {
		const failed = new URLSearchParams(window.location.search).get('error');
		if (failed) {
			echoFailure = describeSignInFailure(failed);
			failureDetail = readFailureDetail(window.location.hash);
			history.replaceState(null, '', window.location.pathname);
			return;
		}
		const fragment = readEchoFragment(window.location.hash);
		if (fragment.kind === 'none') return;

		history.replaceState(null, '', window.location.pathname);

		if (fragment.kind === 'payload') {
			payload = fragment.payload;
		} else {
			readError = 'The echoed claims could not be decoded. Run the echo again.';
		}
	});

	async function handleStart() {
		starting = true;
		startError = null;
		try {
			const { authorization_url } = await startIdentityEcho();
			window.location.href = authorization_url;
		} catch (cause) {
			starting = false;
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			startError = errorMessage(cause);
		}
	}
</script>

<svelte:head><title>Claims echo · ssoossh</title></svelte:head>

<PageShell width="wide">
	<PageHeading title="Claims echo">
		{#snippet sub()}
			What your identity provider actually sends, annotated against the configuration.
		{/snippet}
	</PageHeading>

	<PageSection
		title="How this works"
		description="The server keeps only the claims the configuration maps, so there is no stored copy to show."
		testid="echo-explainer"
	>
		<div class="flex flex-col gap-4 text-sm">
			<p class="text-ink-muted">
				Starting an echo signs you in again with <code>prompt=login</code>, and the callback renders
				the decoded token instead of establishing a session. Your current session is untouched, no
				user record is written, and no login is recorded.
			</p>
			<p class="text-ink-muted">
				The result never leaves this page. It arrives in the URL fragment, which browsers do not
				send to any server, and the address bar is cleared as soon as it has been read. Reload and
				it is gone.
			</p>
			<p class="text-ink-muted">
				It only ever shows <strong>your own</strong> claims. That answers "what fields do I get from this
				identity provider", which is the configuration question. It cannot answer "why is this other person
				missing a group" — the user detail page and the directory probe are for that.
			</p>

			<p class="text-ink-muted">
				Before admin works, <code>ssoosshd identity echo</code> on the server host prints a one-time link
				to the same result, for whoever opens it and with no role needed.
			</p>

			<div>
				<Button testid="echo-start" busy={starting} onclick={handleStart}>
					{starting ? 'Redirecting…' : 'Show me what my IdP sends'}
				</Button>
			</div>

			{#if startError}
				<Alert variant="error" title="Could not start the echo" testid="echo-start-error">
					{startError}
				</Alert>
			{/if}
		</div>
	</PageSection>

	{#if echoFailure}
		<Alert variant="error" title={echoFailure.title} testid="echo-failure">
			{echoFailure.message}
			{#if failureDetail}
				<span
					class="mt-2 block font-mono text-dense wrap-anywhere"
					data-testid="echo-failure-detail">{failureDetail}</span
				>
			{/if}
		</Alert>
	{/if}

	{#if readError}
		<Alert variant="error" title="Could not read the result" testid="echo-read-error">
			{readError}
		</Alert>
	{/if}

	{#if payload}
		<EchoResult {payload} />
	{/if}
</PageShell>
