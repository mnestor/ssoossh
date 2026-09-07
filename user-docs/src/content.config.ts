import { defineCollection } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

// Starlight's own schema, unextended. It briefly carried an `eyebrow` field
// -- the accent label the app used to show above a page's heading -- which
// went with the app's own (frontend/DESIGN.md): on every page here it said
// what the sidebar group already said.
export const collections = {
	docs: defineCollection({
		loader: docsLoader(),
		schema: docsSchema(),
	}),
};
