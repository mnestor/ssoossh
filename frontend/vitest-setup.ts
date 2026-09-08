import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/svelte';
import { afterEach } from 'vitest';

// @testing-library/svelte only auto-cleans when the test globals it looks
// for are present at import time; doing it explicitly means a component left
// mounted by one test cannot be found by the next one's queries.
afterEach(() => cleanup());

// jsdom does not implement <dialog>'s showModal()/close() (every real
// browser does). Without a stub, any component that calls showModal() to
// get native modal behavior throws in tests. This mirrors real behavior
// closely enough for assertions (toggling the `open` attribute, tracking
// whether a modal is active) without pulling in a full dialog polyfill.
if (typeof HTMLDialogElement !== 'undefined') {
	if (!HTMLDialogElement.prototype.showModal) {
		HTMLDialogElement.prototype.showModal = function (this: HTMLDialogElement) {
			this.setAttribute('open', '');
		};
	}
	if (!HTMLDialogElement.prototype.close) {
		// close() fires a `close` event in every real browser, and components
		// rely on it to learn about every close path at once — the button,
		// Escape, the backdrop. A stub that only dropped the attribute would
		// make a component that handles closing correctly look broken.
		HTMLDialogElement.prototype.close = function (this: HTMLDialogElement) {
			this.removeAttribute('open');
			this.dispatchEvent(new Event('close'));
		};
	}
}

// jsdom implements no Web Animations API, and Svelte 5 drives every
// `transition:` through `element.animate()` — so the moment a component with
// an entrance or an exit is rendered, jsdom throws "element.animate is not a
// function" and takes the test with it. Same situation as showModal() above:
// a real browser has this and the test environment does not.
//
// The stub finishes on the next macrotask rather than after the duration it
// was handed. A unit test asserting that a popover closed should not have to
// wait out its 130ms exit to see it, and the thing under test is what the
// component does when the transition ends, not how long it takes to get
// there — the durations themselves are pinned in motion.test.ts.
//
// `currentTime` reports the full duration because Svelte reads it to work
// out how far through the curve it is: reporting the end is what makes the
// tick land on the final value rather than partway.
if (typeof Element !== 'undefined' && !Element.prototype.animate) {
	class StubAnimation {
		onfinish: (() => void) | null = null;
		oncancel: (() => void) | null = null;
		effect: unknown = null;
		playState: 'running' | 'finished' | 'idle' = 'running';
		currentTime: number;
		#timer: ReturnType<typeof setTimeout>;

		constructor(duration: number) {
			this.currentTime = duration;
			this.#timer = setTimeout(() => {
				// cancel() during the wait must not resurrect the animation:
				// Svelte cancels one animation and starts another in the same
				// turn, and a finish callback firing on the dead one runs the
				// outro twice.
				if (this.playState !== 'running') {
					return;
				}
				this.playState = 'finished';
				this.onfinish?.();
			}, 0);
		}

		cancel() {
			this.playState = 'idle';
			clearTimeout(this.#timer);
		}

		finish() {
			this.playState = 'finished';
		}

		play() {}
		pause() {}
	}

	Element.prototype.animate = function (
		_keyframes: unknown,
		options?: number | { duration?: number }
	) {
		const duration = typeof options === 'number' ? options : (options?.duration ?? 0);
		return new StubAnimation(duration) as unknown as Animation;
	} as Element['animate'];
}

// Node 26 (the CI image) injects its own experimental `localStorage` and
// `sessionStorage` into every vm context, overwriting the accessors jsdom
// installed on that same global. Node's read back undefined unless the process
// was started with --localstorage-file, so a bare `localStorage.clear()` in a
// test throws instead of reaching jsdom. jsdom's real Storage objects are
// still there one layer down, so point the globals back at them. On a Node
// that has no such global (22, local dev) they already agree and this is a
// no-op.
const jsdomWindow = globalThis as unknown as {
	_localStorage?: Storage;
	_sessionStorage?: Storage;
};
for (const [name, storage] of [
	['localStorage', jsdomWindow._localStorage],
	['sessionStorage', jsdomWindow._sessionStorage]
] as const) {
	if (storage) {
		Object.defineProperty(globalThis, name, { configurable: true, get: () => storage });
	}
}
