import '@testing-library/jest-dom/vitest';
import { cleanup } from '@testing-library/svelte';
import { afterEach } from 'vitest';

// @testing-library/svelte only auto-cleans when the test globals it looks
// for are present at import time; doing it explicitly means a component left
// mounted by one test cannot be found by the next one's queries.
afterEach(() => cleanup());
