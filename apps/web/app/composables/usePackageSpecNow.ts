/** A serialized render instant keeps cached repository ages and badges stable through hydration. */
export function usePackageSpecNow() {
  return useState<number>('onv:package-spec-now', Date.now);
}
