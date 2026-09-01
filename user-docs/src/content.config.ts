import { defineCollection, z } from 'astro:content';
import { docsLoader } from '@astrojs/starlight/loaders';
import { docsSchema } from '@astrojs/starlight/schema';

export const collections = {
	docs: defineCollection({
		loader: docsLoader(),
		// `eyebrow` is the accent label the app shows above a page's heading
		// (frontend/DESIGN.md); src/components/PageTitle.astro renders it.
		schema: docsSchema({
			extend: z.object({
				eyebrow: z.string().optional(),
			}),
		}),
	}),
};
