#[cfg(target_os = "macos")]
mod mac {
    use objc2::{MainThreadMarker, rc::Retained};
    use objc2_app_kit::NSBitmapImageRep;
    use opennavo_desktop_lib::monitor::{
        menubar::{Graph, TextIcon, render_template},
        types::MonitorModule,
    };

    #[derive(Debug)]
    struct Bounds {
        left: isize,
        top: isize,
        right: isize,
        bottom: isize,
    }

    fn bounds(rep: &NSBitmapImageRep, region: Option<(isize, isize)>) -> Option<Bounds> {
        let mut found = None::<Bounds>;
        let (start, end) = region.unwrap_or((0, rep.pixelsWide()));
        for y in 0..rep.pixelsHigh() {
            for x in start..end {
                if rep.colorAtX_y(x, y).unwrap().alphaComponent() < 0.1 {
                    continue;
                }
                let b = found.get_or_insert(Bounds {
                    left: x,
                    top: y,
                    right: x,
                    bottom: y,
                });
                b.left = b.left.min(x);
                b.top = b.top.min(y);
                b.right = b.right.max(x);
                b.bottom = b.bottom.max(y);
            }
        }
        found
    }

    fn bitmaps(icon: &TextIcon, mtm: MainThreadMarker) -> Vec<Retained<NSBitmapImageRep>> {
        let image = render_template(icon, mtm).unwrap();
        image
            .representations()
            .iter()
            .map(|rep| rep.downcast::<NSBitmapImageRep>().unwrap())
            .collect()
    }

    /// `flush_left` items start at their left edge, like Stats' mini widget, so ink may touch
    /// the first column; everything else must keep clear of both edges.
    fn check(icon: &TextIcon, flush_left: bool, mtm: MainThreadMarker) {
        let image = render_template(icon, mtm).unwrap();
        assert!(image.isTemplate());
        let width = image.size().width;
        assert_eq!(
            width,
            width.round(),
            "whole points keep 1x/2x pixel aligned"
        );
        let reps = image.representations();
        assert_eq!(reps.len(), 2);
        let mut previous = None::<Bounds>;
        for (i, rep) in reps.iter().enumerate() {
            let rep = rep.downcast_ref::<NSBitmapImageRep>().unwrap();
            let scale = (i + 1) as isize;
            assert_eq!(rep.pixelsWide(), width as isize * scale);
            assert_eq!(rep.pixelsHigh(), 22 * scale);
            assert_eq!(rep.size().width, width);
            assert_eq!(rep.size().height, 22.0);
            let b = bounds(rep, None).expect("template must contain visible pixels");
            if flush_left {
                assert!(b.left <= scale, "text must start at the left edge: {b:?}");
                // At 1x the last glyph's side bearing can be under a pixel; at 2x it must clear one.
                assert!(
                    scale == 1 || b.right < rep.pixelsWide() - 1,
                    "text clipped at the right edge: {b:?}"
                );
            } else {
                assert!(
                    b.left > 0 && b.right < rep.pixelsWide() - 1,
                    "horizontal clipping at {scale}x: {b:?}"
                );
            }
            if let Some(p) = previous {
                for (one, two) in [
                    (p.left, b.left),
                    (p.top, b.top),
                    (p.right, b.right),
                    (p.bottom, b.bottom),
                ] {
                    assert!(
                        (one as f64 - two as f64 / 2.0).abs() <= 1.25,
                        "1x/2x content bounds differ: {p:?} vs {b:?}"
                    );
                }
            }
            previous = Some(b);
            if icon.graph.is_some() {
                assert!(
                    bounds(rep, Some((0, 43 * scale))).is_some(),
                    "graph must be visible at {scale}x"
                );
                assert!(
                    bounds(rep, Some((45 * scale, rep.pixelsWide()))).is_some(),
                    "current value must be visible at {scale}x"
                );
            }
        }
        if icon.lines.len() == 2 {
            // Draw each row separately at the same font/position to inspect its own bounds.
            let row = |keep: usize| {
                let mut isolated = icon.clone();
                isolated.lines[1 - keep].clear();
                bitmaps(&isolated, mtm)
                    .iter()
                    .map(|rep| bounds(rep, None).expect("both text rows must be visible"))
                    .collect::<Vec<_>>()
            };
            for (top, bottom) in row(0).into_iter().zip(row(1)) {
                assert!(
                    top.bottom < bottom.top,
                    "first line must sit above the second: {top:?} vs {bottom:?}"
                );
            }
        }
    }

    /// The widest reading must nearly fill the item: AppKit already puts space between items.
    fn check_snug(widest: &TextIcon, narrow: &TextIcon, mtm: MainThreadMarker) {
        let image = render_template(widest, mtm).unwrap();
        assert_eq!(
            render_template(narrow, mtm).unwrap().size().width,
            image.size().width,
            "the width must not follow the reading"
        );
        let rep = image.representations().objectAtIndex(1);
        let rep = rep.downcast_ref::<NSBitmapImageRep>().unwrap();
        let b = bounds(rep, None).unwrap();
        // Allow the side bearing of a "1" on top of the padding.
        assert!(
            b.left <= 8 && rep.pixelsWide() - 1 - b.right <= 8,
            "more than 4 pt of clear space: {b:?} in {} px",
            rep.pixelsWide()
        );
    }

    fn icon(module: MonitorModule, lines: &[&str], widest: &str, graph: Option<Graph>) -> TextIcon {
        TextIcon {
            module,
            lines: lines.iter().map(|line| line.to_string()).collect(),
            widest: widest.into(),
            graph,
        }
    }

    pub fn run() {
        use MonitorModule::{Battery, Cpu, Disk, Network, Sensors};
        let mtm = MainThreadMarker::new().expect("AppKit regression test must run on main thread");
        objc2::rc::autoreleasepool(|_| {
            let line = Graph::Line(vec![Some(0.1), Some(0.8), None, Some(0.4), Some(0.66)]);
            let bars = Graph::Bars(vec![0.95, 0.7, 0.3, 0.1]);
            let fixtures = [
                ("text", icon(Cpu, &["CPU", "66%"], "100%", None), true),
                ("value", icon(Cpu, &["66%"], "100%", None), false),
                (
                    "widest text",
                    icon(Disk, &["DISK", "1850G"], "2000G", None),
                    true,
                ),
                (
                    "full text",
                    icon(Battery, &["BAT", "100%"], "100%", None),
                    true,
                ),
                ("temperature", icon(Sensors, &["82°"], "44°", None), false),
                (
                    "temperature past 99°",
                    icon(Sensors, &["104°"], "44°", None),
                    false,
                ),
                ("line", icon(Cpu, &["66%"], "100%", Some(line)), false),
                ("bars", icon(Cpu, &["66%"], "100%", Some(bars)), false),
                (
                    "network text",
                    icon(Network, &["↑123K", "↓456K"], "↓99.9M", None),
                    false,
                ),
            ];
            let mut failures = 0;
            for (name, icon, flush_left) in fixtures {
                let passed = std::panic::catch_unwind(|| check(&icon, flush_left, mtm)).is_ok();
                println!(
                    "test {name} 1x/2x content ... {}",
                    if passed { "ok" } else { "FAILED" }
                );
                failures += usize::from(!passed);
            }
            let snug = [
                (
                    "text",
                    icon(Cpu, &["CPU", "100%"], "100%", None),
                    icon(Cpu, &["CPU", "0%"], "100%", None),
                ),
                (
                    "value",
                    icon(Cpu, &["100%"], "100%", None),
                    icon(Cpu, &["—"], "100%", None),
                ),
                (
                    "network text",
                    icon(Network, &["↑99.9M", "↓99.9M"], "↓99.9M", None),
                    icon(Network, &["↑0B", "↓1K"], "↓99.9M", None),
                ),
            ];
            for (name, widest, narrow) in snug {
                let passed = std::panic::catch_unwind(|| check_snug(&widest, &narrow, mtm)).is_ok();
                println!(
                    "test {name} fixed snug width ... {}",
                    if passed { "ok" } else { "FAILED" }
                );
                failures += usize::from(!passed);
            }
            // Short readings keep the 31 pt of Stats' mini widget, with or without the label.
            for (name, icon, expected) in [
                ("text", icon(Cpu, &["CPU", "5%"], "5%", None), 31.0),
                ("value", icon(Cpu, &["5%"], "5%", None), 31.0),
            ] {
                let width = render_template(&icon, mtm).unwrap().size().width;
                let passed = width == expected;
                println!(
                    "test {name} Stats minimum width ... {}",
                    if passed {
                        "ok".to_owned()
                    } else {
                        format!("FAILED ({width} pt)")
                    }
                );
                failures += usize::from(!passed);
            }
            // Like Stats' stack widget, a temperature hugs two digits and grows only past 99°.
            let temperature = |reading: &str| {
                render_template(&icon(Sensors, &[reading], "44°", None), mtm)
                    .unwrap()
                    .size()
                    .width
            };
            let (two, other, three) = (temperature("82°"), temperature("17°"), temperature("104°"));
            let passed = two == other && three > two && two <= 30.0;
            println!(
                "test temperature width ... {}",
                if passed {
                    "ok".to_owned()
                } else {
                    format!("FAILED ({two} / {other} / {three} pt)")
                }
            );
            failures += usize::from(!passed);
            assert_eq!(failures, 0, "Retina rendering regression failures");
        });
    }
}

fn main() {
    #[cfg(target_os = "macos")]
    mac::run();
}
