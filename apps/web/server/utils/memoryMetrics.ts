import { getHeapStatistics } from 'node:v8';

/**
 * Process memory gauges for the private metrics listener. Keep these values label-free: the endpoint is
 * scraped frequently and must never create a series per request, URL, or deployment input.
 */
export function renderMemoryMetrics(): string {
  const usage = process.memoryUsage();
  const heapLimit = getHeapStatistics().heap_size_limit;
  const lines = [
    '# HELP opennavo_web_heap_used_bytes V8 JavaScript heap currently in use.',
    '# TYPE opennavo_web_heap_used_bytes gauge',
    `opennavo_web_heap_used_bytes ${usage.heapUsed}`,
    '# HELP opennavo_web_heap_limit_bytes V8 JavaScript heap limit.',
    '# TYPE opennavo_web_heap_limit_bytes gauge',
    `opennavo_web_heap_limit_bytes ${heapLimit}`,
    '# HELP opennavo_web_rss_bytes Resident set size of the web process.',
    '# TYPE opennavo_web_rss_bytes gauge',
    `opennavo_web_rss_bytes ${usage.rss}`,
    '# HELP opennavo_web_external_bytes Memory used by V8 external allocations and ArrayBuffers.',
    '# TYPE opennavo_web_external_bytes gauge',
    `opennavo_web_external_bytes ${usage.external}`
  ];
  return `${lines.join('\n')}\n`;
}
