import { defineCollection } from "astro:content";

const chartsCollection = defineCollection({
  type: "content",
});

export const collections = {
  charts: chartsCollection,
};
