/**
 * splitIntoColumns divides a sequence into `count` contiguous runs, chosen
 * so the heaviest run is as light as it can be.
 *
 * Why contiguous rather than best-fit: the caller's order carries meaning.
 * The effective-configuration screen renders its sections in the order the
 * config file declares them, deliberately, so a reader can follow the file
 * down the page. Packing sections into whichever column happens to be
 * shortest would balance better by a few pixels and destroy that.
 *
 * Contiguity also buys the narrow layout for free: because the runs are in
 * order, stacking them one after another reproduces the original sequence
 * exactly, so the same split renders as one column on a phone with nothing
 * reordered.
 *
 * Returns index arrays rather than items so the caller keeps its own typing,
 * and always returns exactly `count` arrays — trailing ones are empty when
 * there is not enough to go round.
 */
export function splitIntoColumns(weights: number[], count: number): number[][] {
	if (count < 1) {
		return [];
	}
	if (weights.length === 0) {
		return Array.from({ length: count }, () => []);
	}

	// Binary search the smallest per-run ceiling that still fits in `count`
	// runs, then cut at that ceiling. The search space is bounded by the
	// heaviest single item below and the whole sequence above: no ceiling
	// under the heaviest item can hold it, and one run holding everything
	// always fits.
	let low = Math.max(...weights);
	let high = weights.reduce((sum, weight) => sum + weight, 0);
	while (low < high) {
		const mid = Math.floor((low + high) / 2);
		if (runsNeeded(weights, mid) <= count) {
			high = mid;
		} else {
			low = mid + 1;
		}
	}

	const columns: number[][] = [[]];
	let running = 0;
	weights.forEach((weight, index) => {
		// A new run starts only while there is another column to start, so
		// the tail never spills past the last one.
		if (running + weight > low && columns.length < count) {
			columns.push([]);
			running = 0;
		}
		columns[columns.length - 1].push(index);
		running += weight;
	});

	while (columns.length < count) {
		columns.push([]);
	}
	return columns;
}

/** runsNeeded counts the contiguous runs a ceiling of `cap` would produce. */
function runsNeeded(weights: number[], cap: number): number {
	let runs = 1;
	let running = 0;
	for (const weight of weights) {
		if (running + weight > cap) {
			runs++;
			running = weight;
		} else {
			running += weight;
		}
	}
	return runs;
}
