<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolveConsoleCode } from '$lib/api/endpoints';
	import { currentPath, goToLogin, redirectIfUnauthenticated } from '$lib/auth';
	import Alert from '$lib/components/Alert.svelte';
	import Button from '$lib/components/Button.svelte';
	import Card from '$lib/components/Card.svelte';
	import PageHeading from '$lib/components/PageHeading.svelte';
	import { session } from '$lib/session.svelte';
	import {
		CODE_LENGTH,
		describeCodeError,
		formatCode,
		isComplete,
		normalizeCode,
		type CodeFailure
	} from '$lib/consolecode';

	// The other end of a console login. A machine with no browser in front
	// of it — a physical tty, a serial console, a BMC viewer — prints eight
	// characters, and this is where they are typed.
	//
	// Deliberately minimal, and deliberately not a list of pending logins.
	// A code is a lookup key for a login the person typing it is already
	// standing in front of; a screen offering logins to approve would be an
	// invitation to approve someone else's, and a console certificate
	// carries the approver's own accounts.

	// The normalized code (no separator) is the state; the input renders the
	// grouped form of it. Typing "k7m4qp2x", "K7M4-QP2X" or "k7m4 qp2x" all
	// converge on the same eight characters.
	let code = $state('');
	let busy = $state(false);
	let failure = $state<CodeFailure | null>(null);

	// A signed-out visitor is sent to /login on arrival rather than after
	// typing eight characters and being bounced by the submit. Losing what
	// someone squinted at a serial console to read is a bad enough outcome
	// on its own; doing it to a code that dies in about a minute and a half
	// means starting the login over at the machine.
	//
	// /login rather than the identity provider directly: it is where a
	// deployment's consent notice lives, and a notice can only gate a
	// sign-in it stands in front of.
	$effect(() => {
		if (session.resolved && !session.signedIn && !session.error) {
			goToLogin(currentPath());
		}
	});

	const display = $derived(formatCode(code));
	const ready = $derived(isComplete(code) && !busy);

	// Rewriting the value on every keystroke means the caret would otherwise
	// jump to the start; putting it at the end is right for a field that is
	// only ever appended to, and the field is short enough that editing
	// mid-string is not the workflow.
	function onInput(event: Event) {
		const input = event.currentTarget as HTMLInputElement;
		code = normalizeCode(input.value);
		input.value = display;
		input.setSelectionRange(input.value.length, input.value.length);
		// A previous failure describes a code that is no longer in the box.
		failure = null;
	}

	async function submit() {
		if (!isComplete(code)) {
			failure = describeCodeError(new Error(`A code is ${CODE_LENGTH} characters.`));
			return;
		}
		busy = true;
		failure = null;
		try {
			const resolved = await resolveConsoleCode(code);
			// The approval page is where the decision is actually made, and
			// it is the same page every other request type uses. Resolving
			// has already claimed the request for this session, so arriving
			// there cannot be refused for a reason this page did not
			// already show.
			//
			// A client-side navigation on purpose, not a document load. A
			// document GET of /approve/<id> would run the browser-level
			// claim (middleware.ApprovalClaimMiddleware), which exists to
			// let a link scanner burn a phishing link — and there is no
			// link here to scan. Worse, the machine that created the
			// request has held its id since the moment it was created, so
			// whoever is at the console could claim the page first and turn
			// every legitimate approval into a redirect to
			// /approval-unavailable. Identity-level binding, done above by
			// resolving the code, is the control that actually applies.
			// eslint-disable-next-line svelte/no-navigation-without-resolve
			await goto(resolved.approval_url);
		} catch (cause) {
			if (redirectIfUnauthenticated(cause)) {
				return;
			}
			failure = describeCodeError(cause);
			busy = false;
		}
	}
</script>

<svelte:head><title>Console login · ssoossh</title></svelte:head>

<div class="flex w-full max-w-[560px] flex-col gap-4">
	<PageHeading eyebrow="Console login" title="Enter the code on the screen" />

	<Card
		description="A machine with no browser shows a short code when someone logs in at its console. Type it here to see what is being asked for."
		testid="console-code-entry"
	>
		<form
			onsubmit={(event) => {
				event.preventDefault();
				submit();
			}}
		>
			<label class="block text-sm font-medium" for="console-code">Code</label>
			<input
				id="console-code"
				name="code"
				value={display}
				oninput={onInput}
				autocomplete="off"
				autocapitalize="characters"
				autocorrect="off"
				spellcheck="false"
				inputmode="text"
				placeholder="K7M4-QP2X"
				aria-describedby="console-code-hint"
				data-testid="console-code-input"
				class="mt-2 w-full rounded-md border border-border-subtle bg-surface px-4 py-3 text-center font-mono text-2xl tracking-[0.2em] text-ink placeholder:text-ink-muted focus:border-accent focus:outline-none"
			/>
			<p id="console-code-hint" class="mt-2 text-xs text-ink-muted">
				{CODE_LENGTH} characters, in two groups. Case does not matter, and there are no letters I, L or
				O — if you see one, it is the digit next to it.
			</p>

			<div class="mt-5">
				<Button type="submit" full {busy} disabled={!ready} testid="console-code-submit">
					{busy ? 'Checking…' : 'Continue'}
				</Button>
			</div>
		</form>

		{#if failure}
			<div class="mt-4">
				<Alert variant="error" title={failure.title} testid="console-code-failure-{failure.kind}">
					{failure.message}
				</Alert>
			</div>
		{/if}
	</Card>
</div>
