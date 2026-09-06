import { describe, expect, it } from 'vitest';

import { splitIntoColumns } from './columns';

// Test methodology: table-driven over the split, since it is pure. The
// property that matters most is not the exact cut but that the cut is the
// best available one — a split that merely "works" is what left the config
// screen lopsided in the first place — and that the runs stay contiguous,
// which is what lets the same split render as a single ordered column on a
// narrow screen.

/** heaviest is the weight of the largest column, which is what the split
 * exists to minimise. */
function heaviest(weights: number[], columns: number[][]): number {
	return Math.max(...columns.map((c) => c.reduce((sum, i) => sum + weights[i], 0)));
}

describe('splitIntoColumns', () => {
	it('should return one column per requested column', () => {
		expect(splitIntoColumns([1, 2, 3], 2)).toHaveLength(2);
	});

	it('should keep every index exactly once', () => {
		const columns = splitIntoColumns([5, 1, 4, 2, 3], 2);

		expect(columns.flat().sort((a, b) => a - b)).toEqual([0, 1, 2, 3, 4]);
	});

	// Stacking the columns in order has to reproduce the input, because
	// that is what the narrow layout renders.
	it('should keep the runs contiguous and in order', () => {
		const columns = splitIntoColumns([5, 1, 4, 2, 3], 2);

		expect(columns.flat()).toEqual([0, 1, 2, 3, 4]);
	});

	it('should split evenly weighted items down the middle', () => {
		expect(splitIntoColumns([1, 1, 1, 1], 2)).toEqual([
			[0, 1],
			[2, 3]
		]);
	});

	// The real shape of the effective-configuration screen: fifteen sections
	// from one setting to fifty-two. An even split by count would put 129
	// rows against 158; by item count alone it would be 8 sections against
	// 7 and wildly lopsided in height.
	it('should balance the configuration screen by weight, not by count', () => {
		const sections = [18, 8, 23, 52, 16, 12, 36, 5, 19, 6, 5, 48, 35, 3, 1];

		const columns = splitIntoColumns(sections, 2);

		expect(columns[0]).toEqual([0, 1, 2, 3, 4, 5]);
		expect(columns[1]).toEqual([6, 7, 8, 9, 10, 11, 12, 13, 14]);
		expect(heaviest(sections, columns)).toBe(158);
	});

	// One item heavier than an even share cannot be split, so the ceiling
	// is that item and the rest packs around it.
	it('should not strand the tail when one item dominates', () => {
		const columns = splitIntoColumns([100, 1, 1, 1], 2);

		expect(columns).toEqual([[0], [1, 2, 3]]);
	});

	it('should leave later columns empty when there is nothing to fill them', () => {
		expect(splitIntoColumns([7], 2)).toEqual([[0], []]);
	});

	it('should return empty columns for an empty sequence', () => {
		expect(splitIntoColumns([], 3)).toEqual([[], [], []]);
	});

	it('should put everything in one column when only one is asked for', () => {
		expect(splitIntoColumns([4, 9, 2], 1)).toEqual([[0, 1, 2]]);
	});

	// Zero weights must not send the search into an empty range.
	it('should handle a sequence that weighs nothing', () => {
		const columns = splitIntoColumns([0, 0, 0], 2);

		expect(columns.flat()).toEqual([0, 1, 2]);
	});

	it('should return nothing when no columns are asked for', () => {
		expect(splitIntoColumns([1, 2], 0)).toEqual([]);
	});
});
