const modules = import.meta.glob('../../assets/webapps/*.png', {
  eager: true,
  import: 'default'
}) as Record<string, string>;

const icons = new Map(
  Object.entries(modules).map(([path, url]) => [
    path
      .split('/')
      .pop()!
      .replace(/\.png$/, ''),
    url
  ])
);

export function webappIcon(id: string): string | undefined {
  return icons.get(id);
}
