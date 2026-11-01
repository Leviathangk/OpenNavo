import { lifecycleReasonOf, packageInstallState } from '@opennavo/shared';
import type { PackageInstallState } from '@opennavo/shared';
import type { PackagePage } from './packageFacts';
import type { Translate } from './seoTitles';

export interface InstallLifecycle {
  state: PackageInstallState;
  /** Why Homebrew disabled/deprecated the package or OpenNavo paused it; absent while active. */
  notice?: string;
}

/**
 * The reason a lifecycle notice shows, in the order the state itself comes from: the shared
 * `lifecycleReasonOf` holds the order, so the website and the Mac client cannot disagree. A missing reason
 * becomes the localized "no reason" text here, because only a caller with a translator can say it.
 */
export function lifecycleReason(
  pkg: Pick<PackagePage, 'disable' | 'deprecation' | 'homebrewStatus'>,
  state: PackageInstallState,
  t: Translate
): string {
  return lifecycleReasonOf(pkg, state) ?? t('package.noReason');
}

/**
 * One lifecycle guard for the install command (T2): Homebrew's disabled state hides the command;
 * deprecation and OpenNavo's recommendation pause keep it with a warning. The overview's install
 * block, the details body, the breadcrumb banner and the header's mobile notice all read this, so the
 * command never appears in one and disappears in the other, and every notice names the same reason.
 */
export function installLifecycle(pkg: PackagePage, t: Translate): InstallLifecycle {
  const state = packageInstallState(pkg);
  if (state === 'active') return { state };
  const key =
    state === 'disabled'
      ? 'package.disabledNotice'
      : state === 'deprecated'
        ? 'package.deprecatedNotice'
        : 'package.staleNotice';
  return { state, notice: t(key, { reason: lifecycleReason(pkg, state, t) }) };
}
