// Check every target's languages and the lock file offline, without accessing the gateway.
import { flatten, hash, loadTarget, lockFile, read, selection, targetLocales, validate } from './interface-i18n.mjs';

function checkTarget(name, lock) {
  try {
    const target = loadTarget(name),
      source = flatten(target.source),
      english = flatten(target.english);
    for (const { code } of targetLocales(name))
      validate(
        source,
        english,
        flatten(code === 'zh-CN' ? target.source : code === 'en-US' ? target.english : read(target.path(code))),
        code,
        name
      );
    const hashes = Object.fromEntries(Object.entries(source).map(([key, value]) => [key, hash(value)]));
    if (JSON.stringify(lock.targets[name]?.sources) !== JSON.stringify(hashes))
      throw new Error(`Chinese source-text lock is outdated: ${name}; run make i18n-sync ARGS="--targets ${name}"`);
    return Object.keys(source).length;
  } catch (error) {
    if (error.code === 'ENOENT')
      throw new Error(
        `Missing locale file: ${error.path}. Run make i18n-sync ARGS="--targets ${name}" to generate it; developers must author zh-CN and en-US source text manually.`
      );
    throw error;
  }
}

try {
  const lock = lockFile();
  let count = 0;
  for (const name of selection(process.argv.slice(2))) count += checkTarget(name, lock);
  console.log(`Interface i18n validation passed: ${count} keys.`);
} catch (error) {
  console.error(error.message);
  process.exitCode = 1;
}
