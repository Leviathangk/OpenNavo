// Rust adaptation of Sparkle's SUStandardVersionComparator (MIT).
// Copyright (c) 2006-2013 Andy Matuschak and Sparkle contributors.
// Full permission notice: LICENSE.sparkle; pinned source: docs/legal/native/sparkle-upstream.json.
use std::cmp::Ordering;

#[derive(Clone, Copy, PartialEq)]
enum Kind {
    Number,
    Text,
    Period,
    Separator,
    Dash,
}
fn kind(c: char) -> Kind {
    if c == '.' {
        return Kind::Period;
    }
    if c == '-' {
        return Kind::Dash;
    }
    if c.is_ascii_digit() {
        return Kind::Number;
    }
    #[cfg(target_os = "macos")]
    {
        use objc2_foundation::NSCharacterSet;
        // Sparkle classifies the first UTF-16 unit, including for supplementary
        // Unicode characters. Use Foundation's exact Unicode character sets.
        let first = c.encode_utf16(&mut [0; 2])[0];
        if NSCharacterSet::decimalDigitCharacterSet().characterIsMember(first) {
            return Kind::Number;
        }
        if NSCharacterSet::whitespaceAndNewlineCharacterSet().characterIsMember(first)
            || NSCharacterSet::punctuationCharacterSet().characterIsMember(first)
        {
            return Kind::Separator;
        }
    }
    #[cfg(not(target_os = "macos"))]
    if c.is_whitespace() || c.is_ascii_punctuation() {
        return Kind::Separator;
    }
    Kind::Text
}
fn parts(version: &str) -> Vec<String> {
    let mut result: Vec<String> = Vec::new();
    let mut previous = None;
    for c in version.chars() {
        let current = kind(c);
        if current == Kind::Dash && previous.is_some() {
            break;
        }
        if previous
            .is_none_or(|old| old != current || matches!(old, Kind::Period | Kind::Separator))
        {
            result.push(c.to_string());
        } else if let Some(part) = result.last_mut() {
            part.push(c);
        }
        previous = Some(current);
    }
    result
}
fn part_kind(part: &str) -> Kind {
    kind(part.chars().next().unwrap_or('.'))
}
fn number(part: &str) -> i64 {
    #[cfg(target_os = "macos")]
    {
        objc2_foundation::NSString::from_str(part).longLongValue()
    }
    #[cfg(not(target_os = "macos"))]
    {
        part.parse().unwrap_or(i64::MAX)
    }
}
fn text_order(a: &str, b: &str) -> Ordering {
    #[cfg(target_os = "macos")]
    {
        use objc2_foundation::{NSComparisonResult, NSString};
        match NSString::from_str(a).compare(&NSString::from_str(b)) {
            NSComparisonResult::Ascending => Ordering::Less,
            NSComparisonResult::Descending => Ordering::Greater,
            _ => Ordering::Equal,
        }
    }
    #[cfg(not(target_os = "macos"))]
    {
        a.cmp(b)
    }
}
pub fn compare(a: &str, b: &str) -> Ordering {
    let mut a = parts(a);
    let mut b = parts(b);
    let prefix = |parts: &[String]| {
        parts
            .iter()
            .take_while(|p| matches!(part_kind(p), Kind::Number | Kind::Period))
            .count()
    };
    let (an, bn) = (prefix(&a), prefix(&b));
    let (short, long, n) = if an < bn {
        (&mut a, &b, an)
    } else {
        (&mut b, &a, bn)
    };
    let additions: Vec<_> = long
        .iter()
        .take(an.max(bn))
        .skip(n)
        .map(|p| {
            if part_kind(p) == Kind::Period {
                ".".to_owned()
            } else {
                "0".to_owned()
            }
        })
        .collect();
    short.splice(n..n, additions);
    for (ap, bp) in a.iter().zip(&b) {
        let (ak, bk) = (part_kind(ap), part_kind(bp));
        let equal_class = ak == bk
            || (matches!(ak, Kind::Period | Kind::Separator)
                && matches!(bk, Kind::Period | Kind::Separator));
        let order = if equal_class {
            match ak {
                Kind::Number => number(ap).cmp(&number(bp)),
                Kind::Text => text_order(ap, bp),
                _ => Ordering::Equal,
            }
        } else if ak == Kind::Text {
            Ordering::Less
        } else if bk == Kind::Text || ak == Kind::Number {
            Ordering::Greater
        } else {
            Ordering::Less
        };
        if order != Ordering::Equal {
            return order;
        }
    }
    match a.len().cmp(&b.len()) {
        Ordering::Equal => Ordering::Equal,
        Ordering::Greater => {
            if part_kind(&a[b.len()]) == Kind::Text {
                Ordering::Less
            } else {
                Ordering::Greater
            }
        }
        Ordering::Less => {
            if part_kind(&b[a.len()]) == Kind::Text {
                Ordering::Greater
            } else {
                Ordering::Less
            }
        }
    }
}

pub fn major(from: &str, to: &str) -> bool {
    let first = |s: &str| s.split('.').next().and_then(|v| v.parse::<u64>().ok());
    first(from).zip(first(to)).is_some_and(|(a, b)| b > a)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn sparkle_build_versions_prereleases_padding_and_suffixes() {
        for (a, b) in [("1.0", "1.0.0"), ("1.0-foo", "1.0-bar"), ("1.01", "1.1")] {
            assert_eq!(compare(a, b), Ordering::Equal, "{a} / {b}");
        }
        for (a, b) in [
            ("1.0b2", "1.0b10"),
            ("1.0rc1", "1.0"),
            ("1.9", "1.10"),
            ("1.0.0b1", "1.0.1"),
            ("9", "10"),
            ("", "1"),
        ] {
            assert_eq!(compare(a, b), Ordering::Less, "{a} / {b}");
            assert_eq!(compare(b, a), Ordering::Greater);
        }
    }
}

/// Electron's published and installed versions use SemVer, including prerelease precedence.
/// Invalid SemVer is not silently interpreted as a Sparkle build number.
pub fn compare_source(
    source: crate::model::UpdateSource,
    a: &str,
    b: &str,
) -> Result<Ordering, crate::AppError> {
    if source != crate::model::UpdateSource::Electron {
        return Ok(compare(a, b));
    }
    let parse = |s: &str| {
        semver::Version::parse(s.trim().strip_prefix('v').unwrap_or(s.trim()))
            .map_err(|_| crate::AppError::new("E_UPDATE_FEED", "electron_semver"))
    };
    Ok(parse(a)?.cmp_precedence(&parse(b)?))
}

#[cfg(test)]
mod source_tests {
    use super::*;
    use crate::model::UpdateSource;
    #[test]
    fn electron_prereleases_and_stable_use_semver_without_changing_sparkle() {
        for (old, new) in [
            ("2.0.0-beta.1", "2.0.0-beta.2"),
            ("2.0.0-beta.2", "2.0.0-beta.10"),
            ("2.0.0-beta.2", "2.0.0"),
        ] {
            assert_eq!(
                compare_source(UpdateSource::Electron, new, old).unwrap(),
                Ordering::Greater
            );
            assert_eq!(
                compare_source(UpdateSource::Electron, old, new).unwrap(),
                Ordering::Less
            );
            assert_eq!(
                compare_source(UpdateSource::Sparkle, new, old).unwrap(),
                Ordering::Equal
            );
        }
        assert_eq!(
            compare_source(UpdateSource::Electron, "2.0.0+build.2", "2.0.0+build.1").unwrap(),
            Ordering::Equal
        );
        assert!(compare_source(UpdateSource::Electron, "invalid", "2.0.0").is_err());
    }
}
