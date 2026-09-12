import type { HomeSectionDefinition } from './provider-registry';

// These are search intents, not bundled tracks. Every result is fetched from
// an enabled Extension metadata provider at runtime.
export const HOME_SECTION_DEFINITIONS: HomeSectionDefinition[] = [
  { id: 'trending', query: 'trending' },
  { id: 'remix', query: 'remix' },
  { id: 'vinahouse', query: 'vinahouse' },
  { id: 'vietnamese', query: 'nhạc Việt' },
  { id: 'edm', query: 'EDM' },
  { id: 'chill', query: 'chill' },
];
