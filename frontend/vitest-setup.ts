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
