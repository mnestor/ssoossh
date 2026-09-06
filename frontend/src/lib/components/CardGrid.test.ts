import { render, screen } from '@testing-library/svelte';
import { createRawSnippet } from 'svelte';
import { describe, expect, it } from 'vitest';

import CardGrid from './CardGrid.svelte';

// Test methodology: the grid is column classes and nothing else, so what is
// pinned is that it does not eat or reorder what it is given, and that the
// single-column default survives — a grid that folded the wrong way would
// put two cards side by side on a phone.

// createRawSnippet renders one root element, so the two cards arrive inside
// a wrapper. The grid does not care what its children are, only that it
// passes them through untouched.
const children = createRawSnippet(() => ({
	render: () => '<div><article>first</article><article>second</article></div>'
}));

describe('card grid', () => {
	it('should render every card it is given', () => {
		render(CardGrid, { children, testid: 'grid' });

		expect(screen.getByText('first')).toBeInTheDocument();
		expect(screen.getByText('second')).toBeInTheDocument();
	});

	it('should keep the cards in the order they were given', () => {
		render(CardGrid, { children, testid: 'grid' });

		const cards = screen.getByTestId('grid').querySelectorAll('article');
		expect([...cards].map((card) => card.textContent)).toEqual(['first', 'second']);
	});

	// One column is the floor, not the fold: the second column only appears
	// once the viewport can carry the rail and two cards.
	it('should start at one column and widen only at xl', () => {
		render(CardGrid, { children, testid: 'grid' });

		const grid = screen.getByTestId('grid');
		expect(grid).toHaveClass('grid-cols-1');
		expect(grid).toHaveClass('xl:grid-cols-2');
	});
});
