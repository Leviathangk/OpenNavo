import { getResponseHeader, setHeader, type H3Event } from 'h3';

/** Add a value without discarding existing links or cache variation fields. */
export function appendHeaderValue(event: H3Event, name: string, value: string) {
  const previous = getResponseHeader(event, name);
  const values = (Array.isArray(previous) ? previous : previous ? [previous] : []).map(String);
  if (!values.some(item => item.toLowerCase() === value.toLowerCase())) values.push(value);
  setHeader(event, name, values.join(', '));
}
