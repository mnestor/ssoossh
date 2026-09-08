import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

import { EXIT_MS, ENTER_MS, easeEnter, easeExit, enterMs, exitMs } from './motion';

describe('motion', () => {
	// The numbers themselves, because they are the contract the whole app is
	// drawn against and a stray edit to one of them is not obvious anywhere
	// else. 220ms sits inside the 200-300ms band an entrance has to stay in.
	it('should keep the entrance inside the 200 to 300ms band', () => {
		expect(ENTER_MS).toBeGreaterThanOrEqual(200);
		expect(ENTER_MS).toBeLessThanOrEqual(300);
	});

	it('should make the exit roughly 40 percent faster than the entrance', () => {
		expect(EXIT_MS / ENTER_MS).toBeCloseTo(0.6, 1);
	});

	// jsdom provides no `matchMedia` at all unless a test installs one, which
	// is the "nobody has asked for anything" branch: both helpers hand back
	// the full duration. The stubbed case below covers the other.
	it('should hand the entrance duration through when motion is not reduced', () => {
		expect(enterMs()).toBe(ENTER_MS);
	});

	it('should hand the exit duration through when motion is not reduced', () => {
		expect(exitMs()).toBe(EXIT_MS);
	});

	// An easing curve is only worth naming if it is not linear: linear is for
	// a spinner and a shimmer, and for nothing that starts or stops.
	const curves: [string, (t: number) => number][] = [
		['easeEnter', easeEnter],
		['easeExit', easeExit]
	];

	for (const [name, curve] of curves) {
		it(`should give ${name} a curve rather than a straight line`, () => {
			expect(curve(0.5)).not.toBeCloseTo(0.5, 2);
		});

		it(`should pin ${name} at both ends`, () => {
			expect(curve(0)).toBeCloseTo(0, 5);
			expect(curve(1)).toBeCloseTo(1, 5);
		});
	}

	// Out decelerates, in accelerates. Half way through, an ease-out is more
	// than half done and an ease-in is less.
	it('should decelerate on the way in and accelerate on the way out', () => {
		expect(easeEnter(0.5)).toBeGreaterThan(0.5);
		expect(easeExit(0.5)).toBeLessThan(0.5);
	});

	// A Svelte transition is driven from JavaScript, so the CSS
	// `prefers-reduced-motion` block cannot reach it. Zero rather than "no
	// transition": the element still mounts and unmounts through the same
	// code path.
	describe('when the reader has asked for reduced motion', () => {
		beforeEach(() => {
			vi.stubGlobal('matchMedia', (query: string) => ({
				matches: query.includes('prefers-reduced-motion'),
				media: query,
				addEventListener: vi.fn(),
				removeEventListener: vi.fn()
			}));
		});

		afterEach(() => {
			vi.unstubAllGlobals();
		});

		it('should collapse the entrance to zero', () => {
			expect(enterMs()).toBe(0);
		});

		it('should collapse the exit to zero', () => {
			expect(exitMs()).toBe(0);
		});
	});
});
