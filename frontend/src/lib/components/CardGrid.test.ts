import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it } from 'vitest';

import CardGrid from './CardGrid.svelte';

// Test methodology: render the grid over named items and read back which
// column each landed in. What is pinned is the thing two earlier layouts got
// wrong — that a tall card does not push the cards beside it around, and
// that a short set does not leave half the width empty — plus the ordering
// guarantee the narrow layout depends on.

interface Item {
	name: string;
	rows: number;
}

// testing-library's `render` does not carry a component's generic through,
// so the grid's `T` arrives here as `unknown` however the items are typed.
// The narrowing is the harness boundary, not a claim about the component:
// the real call sites in .svelte files infer `T` from `items` and are
// checked properly.
const asItem = (value: unknown): Item => value as Item;

/** card renders one item, tagged so a test can find it and read its column. */
const card = createRawSnippet((item: () => unknown) => ({
	render: () => `<article data-testid="card">${asItem(item()).name}</article>`
}));

/** columnsOf reads back the rendered layout as names per column. */
function columnsOf(testid: string): string[][] {
	const root = screen.getByTestId(testid);
	return [...root.children].map((column) =>
		[...column.querySelectorAll('[data-testid="card"]')].map((el) => el.textContent ?? '')
	);
}

const sized = (name: string, rows: number): Item => ({ name, rows });

describe('card grid', () => {
	it('should render every card it is given', () => {
		render(CardGrid, {
			testid: 'grid',
			items: [sized('first', 1), sized('second', 1)],
			card
		});

		expect(screen.getByText('first')).toBeInTheDocument();
		expect(screen.getByText('second')).toBeInTheDocument();
	});

	// Stacking the columns reproduces the input, which is what makes the
	// single-column layout below `xl` come out in the given order.
	it('should keep the cards in the order they were given', () => {
		render(CardGrid, {
			testid: 'grid',
			items: [sized('a', 1), sized('b', 1), sized('c', 1), sized('d', 1)],
			card
		});

		expect(columnsOf('grid').flat()).toEqual(['a', 'b', 'c', 'd']);
	});

	it('should split evenly weighted cards down the middle', () => {
		render(CardGrid, {
			testid: 'grid',
			items: [sized('a', 1), sized('b', 1), sized('c', 1), sized('d', 1)],
			card
		});

		expect(columnsOf('grid')).toEqual([
			['a', 'b'],
			['c', 'd']
		]);
	});

	// The failure that sent this through two rewrites: the config screen's
	// sections run from one setting to fifty-two, and splitting by card
	// count puts a wall of keys beside almost nothing.
	it('should balance by weight rather than by card count', () => {
		render(CardGrid, {
			testid: 'grid',
			items: [sized('huge', 50), sized('a', 1), sized('b', 1), sized('c', 1)],
			card,
			weight: (item: unknown) => asItem(item).rows
		});

		expect(columnsOf('grid')).toEqual([['huge'], ['a', 'b', 'c']]);
	});

	// An empty column still claims half the width if it is rendered, which
	// is how one card ends up looking like a half-empty page.
	it('should not render a column with nothing in it', () => {
		render(CardGrid, { testid: 'grid', items: [sized('only', 1)], card });

		expect(columnsOf('grid')).toEqual([['only']]);
	});

	it('should render nothing at all for an empty set', () => {
		render(CardGrid, { testid: 'grid', items: [], card });

		expect(columnsOf('grid')).toEqual([]);
	});

	// One column is the floor, not the fold: the second appears only once
	// the viewport can carry the rail and two cards.
	it('should stack into one column below xl', () => {
		render(CardGrid, { testid: 'grid', items: [sized('a', 1), sized('b', 1)], card });

		const grid = screen.getByTestId('grid');
		expect(grid).toHaveClass('flex-col');
		expect(grid).toHaveClass('xl:flex-row');
	});
});
