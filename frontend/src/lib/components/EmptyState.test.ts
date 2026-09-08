import { render, screen } from '@testing-library/svelte';
import { describe, expect, it } from 'vitest';

import EmptyState from './EmptyState.svelte';
import { createRawSnippet } from 'svelte';

/** text builds the snippet a caller would pass as body or action content. */
function text(value: string) {
	return createRawSnippet(() => ({ render: () => `<span>${value}</span>` }));
}

describe('EmptyState', () => {
	it('should render the title', () => {
		render(EmptyState, { icon: 'certificate-off', title: 'No certificates yet' });
		expect(screen.getByText('No certificates yet')).toBeInTheDocument();
	});

	it('should render the body when one is given', () => {
		render(EmptyState, {
			icon: 'certificate-off',
			title: 'No certificates yet',
			children: text('Run ssoossh login to request one.')
		});
		expect(screen.getByText('Run ssoossh login to request one.')).toBeInTheDocument();
	});

	it('should render the action when one is given', () => {
		render(EmptyState, {
			icon: 'filter-off',
			title: 'No certificates match',
			action: text('Clear filters')
		});
		expect(screen.getByText('Clear filters')).toBeInTheDocument();
	});

	it('should render no body paragraph when none is given', () => {
		const { container } = render(EmptyState, { icon: 'users', title: 'No users found' });
		expect(container.querySelectorAll('p')).toHaveLength(1);
	});

	it('should carry the testid onto its root element', () => {
		render(EmptyState, { icon: 'users', title: 'No users found', testid: 'users-empty' });
		expect(screen.getByTestId('users-empty')).toBeInTheDocument();
	});

	// The title says what the icon says, so the icon is decoration. Giving it
	// an accessible name would make a reader hear the state twice.
	it('should hide the icon from assistive technology', () => {
		const { container } = render(EmptyState, { icon: 'users', title: 'No users found' });
		expect(container.querySelector('svg')).toHaveAttribute('aria-hidden', 'true');
	});
});
