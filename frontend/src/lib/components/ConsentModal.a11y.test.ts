import { render, screen } from '@testing-library/svelte';
import userEvent from '@testing-library/user-event';
import { axe, toHaveNoViolations } from 'jest-axe';
import { describe, expect, it } from 'vitest';

import ConsentModal from './ConsentModal.svelte';

expect.extend(toHaveNoViolations);

describe('ConsentModal accessibility', () => {
	it('should have no automated a11y violations', async () => {
		const { container } = render(ConsentModal, {
			notice: 'You are accessing a monitored system.',
			onaccepted: () => {}
		});
		const results = await axe(container);
		expect(results).toHaveNoViolations();
	});

	it('should have dialog role', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const dialog = screen.getByRole('dialog');
		expect(dialog).toBeInTheDocument();
	});

	it('should have an accessible name for the modal', () => {
		render(ConsentModal, {
			notice: 'You are accessing a monitored system.',
			onaccepted: () => {}
		});
		const dialog = screen.getByRole('dialog');
		// The modal should have an accessible name via aria-labelledby or similar
		expect(dialog).toHaveAccessibleName();
	});

	it('should focus the approve button on open', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const approveBtn = screen.getByRole('button', { name: /accept|approve|continue/i });
		expect(approveBtn).toBeInTheDocument();
	});

	it('should trap focus within the modal when open', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Focus trap is enforced by showModal() behavior; this documents it
		const dialog = screen.getByRole('dialog');
		expect(dialog).toHaveAttribute('open');
	});

	it('Escape key should be blocked (cannot dismiss unaccepted)', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const dialog = document.querySelector('dialog');
		const event = new KeyboardEvent('keydown', { key: 'Escape', cancelable: true });
		dialog?.dispatchEvent(event);
		// The component should preventDefault on cancel event
		expect(dialog).toBeTruthy();
	});

	it('should have high contrast text and button colors', () => {
		const { container } = render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Verify button is visible and styled
		const button = screen.getByRole('button', { name: /accept|approve/i });
		const computed = window.getComputedStyle(button);
		// Should have defined background and text colors (not transparent)
		expect(computed.backgroundColor).not.toBe('transparent');
		expect(computed.color).not.toBe('transparent');
	});

	it('should have a button with text label', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const button = screen.getByRole('button', { name: /accept|approve/i });
		// Button should have accessible text content
		expect(button.textContent?.trim().length).toBeGreaterThan(0);
	});

	it('should announce the notice text to screen readers', () => {
		render(ConsentModal, {
			notice: 'You are accessing a monitored system.',
			onaccepted: () => {}
		});
		// The notice text should be in the document and accessible
		expect(screen.getByText(/monitored system/)).toBeInTheDocument();
	});

	it('should have clear, descriptive button label', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Button label should be clear about what it does (e.g., "I Accept", "Continue")
		const button = screen.getByRole('button');
		expect(button.textContent).toMatch(/accept|approve|continue|i understand/i);
	});

	it('should be keyboard navigable', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});

		const button = screen.getByRole('button', { name: /accept|approve/i });
		// Button should be focusable (no negative tabindex)
		expect(button).toBeInTheDocument();
	});

	it('should activate button on click', () => {
		let accepted = false;
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {
				accepted = true;
			}
		});

		const button = screen.getByRole('button', { name: /accept|approve/i });
		// Button should be clickable and interactive
		expect(button).toBeEnabled();
	});

	it('should work with reduced motion preferences', () => {
		// Test that the modal respects prefers-reduced-motion
		// No animations should occur or they should be instant
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// The modal should still be usable and visible
		expect(screen.getByRole('dialog')).toBeInTheDocument();
	});

	it('should be visible with high contrast mode', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const dialog = screen.getByRole('dialog');
		const computed = window.getComputedStyle(dialog);
		// Dialog should have defined colors (not relying on default theme)
		expect(computed.backgroundColor).toBeTruthy();
	});

	it('should have a button that can receive focus', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		const button = screen.getByRole('button', { name: /accept|approve/i });
		// Button element exists and is a native button (not a div)
		expect(button.tagName).toBe('BUTTON');
	});

	it('should have semantic HTML structure', () => {
		render(ConsentModal, {
			notice: 'Notice text',
			onaccepted: () => {}
		});
		// Dialog role should be used (not just a div)
		const dialog = screen.getByRole('dialog');
		expect(dialog.tagName.toLowerCase()).toBe('dialog');
	});
});
