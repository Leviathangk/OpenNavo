use super::{sampler::Publication, types::*};
use crate::AppError;
#[derive(Clone, Debug, PartialEq)]
pub struct TextIcon {
    pub module: MonitorModule,
    pub lines: Vec<String>,
    /// The widest value the module can show. The item is sized to fit it, not the current
    /// reading, so it keeps one width and its neighbors never shift.
    pub widest: String,
    pub graph: Option<Graph>,
    /// One status item for several modules: a single compact line, no per-module graph.
    pub combined: bool,
}
#[derive(Clone, Debug, PartialEq)]
pub enum Graph {
    Line(Vec<Option<f64>>),
    Bars(Vec<f64>),
}
/// At most three digits ("8.5M", "12.3M", "120M"), so the network item has a fixed width.
fn rate(n: f64) -> String {
    let (value, unit) = [(1e9, 'G'), (1e6, 'M'), (1e3, 'K')]
        .into_iter()
        .find(|(scale, _)| n >= scale * 0.9995)
        .map_or((n, 'B'), |(scale, unit)| (n / scale, unit));
    if unit != 'K' && unit != 'B' && value < 99.95 {
        format!("{value:.1}{unit}")
    } else {
        format!("{value:.0}{unit}")
    }
}
pub fn icon_text(module: MonitorModule, style: MenuBarStyle, s: &MonitorSample) -> TextIcon {
    let percent = |ratio: f64| format!("{:.0}%", ratio * 100.0);
    let root = s.disk.volumes.iter().find(|v| v.mount_point == "/");
    let value = match module {
        MonitorModule::Cpu => percent(f64::from(s.cpu.usage)),
        MonitorModule::Memory => percent(if s.memory.total_bytes > 0 {
            s.memory.used_bytes as f64 / s.memory.total_bytes as f64
        } else {
            0.0
        }),
        MonitorModule::Disk => root
            .map(|v| format!("{:.0}G", v.free_bytes as f64 / 1e9))
            .unwrap_or_else(|| "—".into()),
        MonitorModule::Network => format!("↓{}", rate(f64::from(s.network.down_bytes_per_sec))),
        MonitorModule::Battery => s
            .battery
            .as_ref()
            .map(|b| percent(f64::from(b.percent)))
            .unwrap_or_else(|| "—".into()),
        MonitorModule::Gpu => s
            .gpu
            .as_ref()
            .map(|g| percent(f64::from(g.usage)))
            .unwrap_or_else(|| "—".into()),
        MonitorModule::Sensors => s
            .sensors
            .as_ref()
            .and_then(|s| s.iter().map(|s| f64::from(s.celsius)).reduce(f64::max))
            .map(|t| format!("{t:.0}°"))
            .unwrap_or_else(|| "—".into()),
        MonitorModule::Fans => s
            .fans
            .as_ref()
            .and_then(|fans| {
                (!fans.is_empty()).then(|| {
                    fans.iter().map(|f| u64::from(f.rpm)).sum::<u64>() as f64 / fans.len() as f64
                })
            })
            .map(|rpm| format!("{rpm:.0}"))
            .unwrap_or_else(|| "—".into()),
    };
    // Free space never exceeds the disk size. A temperature is sized for two digits ("44°" is the
    // widest) and grows only past 99°.
    let widest = match module {
        MonitorModule::Cpu
        | MonitorModule::Memory
        | MonitorModule::Gpu
        | MonitorModule::Battery => "100%".into(),
        MonitorModule::Disk => root
            .map(|v| format!("{:.0}G", v.total_bytes as f64 / 1e9))
            .unwrap_or_default(),
        MonitorModule::Network => "↓99.9M".into(),
        MonitorModule::Sensors => "44°".into(),
        MonitorModule::Fans => "0000".into(),
    };
    let label = match module {
        MonitorModule::Cpu => Some("CPU"),
        MonitorModule::Memory => Some("RAM"),
        MonitorModule::Disk => Some("DISK"),
        MonitorModule::Battery => Some("BAT"),
        MonitorModule::Gpu => Some("GPU"),
        MonitorModule::Fans => Some("RPM"),
        // Network shows upload over download, and a temperature reads on its own, as in Stats.
        MonitorModule::Network | MonitorModule::Sensors => None,
    };
    let lines = match label {
        _ if style == MenuBarStyle::Value => vec![value],
        _ if module == MonitorModule::Network => vec![
            format!("↑{}", rate(f64::from(s.network.up_bytes_per_sec))),
            value,
        ],
        Some(label) => vec![label.into(), value],
        None => vec![value],
    };
    TextIcon {
        module,
        lines,
        widest,
        graph: None,
        combined: false,
    }
}

/// One compact reading from the enabled modules, rotating through them in menu bar order. Labels match
/// the two-line items; a temperature stays bare. The selected module's widest variant keeps the item
/// from moving as its reading changes; the width follows the visible module.
pub fn combined_text(
    menu: &MonitorMenuBar,
    modules: &[MonitorModule],
    s: &MonitorSample,
    index: usize,
) -> TextIcon {
    let label = |module: MonitorModule| match module {
        MonitorModule::Cpu => "CPU",
        MonitorModule::Gpu => "GPU",
        MonitorModule::Memory => "RAM",
        MonitorModule::Disk => "DISK",
        MonitorModule::Network => "NET",
        MonitorModule::Battery => "BAT",
        MonitorModule::Sensors | MonitorModule::Fans => "",
    };
    let part = |module: MonitorModule| {
        let value = icon_text(module, MenuBarStyle::Value, s);
        let label = label(module);
        let current = value.lines.first().cloned().unwrap_or_default();
        let widest = value.widest;
        (
            if label.is_empty() {
                current
            } else {
                format!("{label} {current}")
            },
            if label.is_empty() {
                widest
            } else {
                format!("{label} {widest}")
            },
        )
    };
    let shown: Vec<_> = modules
        .iter()
        .copied()
        .filter(|module| menu.item(*module).enabled)
        .collect();
    let parts: Vec<_> = shown.iter().map(|module| part(*module)).collect();
    let selected = if parts.is_empty() {
        0
    } else {
        index % parts.len()
    };
    let current = parts
        .get(selected)
        .map_or_else(String::new, |(value, _)| value.clone());
    let widest = parts
        .get(selected)
        .map_or_else(String::new, |(_, maximum)| maximum.clone());
    TextIcon {
        module: shown.get(selected).copied().unwrap_or(MonitorModule::Cpu),
        lines: vec![current],
        widest,
        graph: None,
        combined: true,
    }
}
pub fn icon(
    module: MonitorModule,
    style: MenuBarStyle,
    s: &MonitorSample,
    history: &[MonitorSample],
) -> TextIcon {
    if !matches!(style, MenuBarStyle::Line | MenuBarStyle::Bars)
        || matches!(module, MonitorModule::Disk | MonitorModule::Battery)
    {
        return icon_text(module, style, s);
    }
    let mut text = icon_text(module, MenuBarStyle::Value, s);
    let main = |s: &MonitorSample| -> Option<f64> {
        match module {
            MonitorModule::Cpu => Some(f64::from(s.cpu.usage)),
            MonitorModule::Gpu => s.gpu.as_ref().map(|g| f64::from(g.usage)),
            MonitorModule::Memory => (s.memory.total_bytes > 0)
                .then(|| s.memory.used_bytes as f64 / s.memory.total_bytes as f64),
            MonitorModule::Network => Some(f64::from(s.network.down_bytes_per_sec)),
            MonitorModule::Sensors => s.sensors.as_ref().and_then(|v| {
                v.iter()
                    .map(|r| f64::from(r.celsius) / 100.0)
                    .reduce(f64::max)
            }),
            MonitorModule::Fans => s
                .fans
                .as_ref()
                .filter(|v| !v.is_empty())
                .map(|v| v.iter().map(|f| f64::from(f.rpm)).sum::<f64>() / v.len() as f64),
            _ => None,
        }
    };
    let recent = &history[history.len().saturating_sub(30)..];
    let network_max = recent
        .iter()
        .flat_map(|s| {
            [
                f64::from(s.network.down_bytes_per_sec),
                f64::from(s.network.up_bytes_per_sec),
            ]
        })
        .chain([
            f64::from(s.network.down_bytes_per_sec),
            f64::from(s.network.up_bytes_per_sec),
        ])
        .fold(0.0_f64, f64::max)
        .max(1.0);
    let fan_max = recent
        .iter()
        .chain(std::iter::once(s))
        .filter_map(|s| {
            s.fans
                .as_ref()
                .filter(|v| !v.is_empty())
                .map(|v| v.iter().map(|f| f64::from(f.max_rpm)).sum::<f64>() / v.len() as f64)
        })
        .fold(0.0_f64, f64::max)
        .max(1.0);
    text.graph = Some(if style == MenuBarStyle::Line {
        let mut values = Vec::new();
        let start = 30usize.saturating_sub(recent.len());
        values.resize(start, None);
        for (i, sample) in recent.iter().enumerate() {
            if i > 0
                && sample.at - recent[i - 1].at > 6000
                && let Some(last) = values.last_mut()
            {
                *last = None;
            }
            values.push(main(sample).map(|v| {
                (if module == MonitorModule::Network {
                    v / network_max
                } else if module == MonitorModule::Fans {
                    v / fan_max
                } else {
                    v
                })
                .clamp(0.0, 1.0)
            }));
        }
        Graph::Line(values)
    } else {
        Graph::Bars(match module {
            MonitorModule::Cpu => s.cpu.cores.iter().copied().map(f64::from).collect(),
            MonitorModule::Network => vec![
                f64::from(s.network.down_bytes_per_sec) / network_max,
                f64::from(s.network.up_bytes_per_sec) / network_max,
            ],
            MonitorModule::Fans => s
                .fans
                .as_ref()
                .map(|v| {
                    v.iter()
                        .map(|f| {
                            if f.max_rpm > 0 {
                                (f64::from(f.rpm) / f64::from(f.max_rpm)).clamp(0.0, 1.0)
                            } else {
                                0.0
                            }
                        })
                        .collect()
                })
                .unwrap_or_default(),
            _ => main(s).into_iter().map(|v| v.clamp(0.0, 1.0)).collect(),
        })
    });
    text
}
#[cfg(target_os = "macos")]
mod mac {
    use super::*;
    use objc2::{
        AnyThread, DefinedClass, MainThreadMarker, MainThreadOnly, define_class, rc::Retained, sel,
    };
    use objc2_app_kit::*;
    use objc2_foundation::{
        NSAffineTransform, NSDictionary, NSObject, NSObjectProtocol, NSPoint, NSRect, NSSize,
        NSString,
    };
    use std::{cell::RefCell, collections::BTreeMap};
    struct TargetData {
        app: tauri::AppHandle,
        modules: RefCell<Vec<MonitorModule>>,
        selected: RefCell<MonitorModule>,
        combined: bool,
    }
    define_class!(
        #[unsafe(super(NSObject))]
        #[thread_kind=MainThreadOnly]
        #[name="OpenNavoMonitorStatusTarget"]
        #[ivars=TargetData]
        struct Target;
        unsafe impl NSObjectProtocol for Target {}
        impl Target {
            #[unsafe(method(clicked:))]
            fn clicked(&self,sender:&NSStatusBarButton) {
                crate::tray::panel::status_click();
                let Some(window)=sender.window() else {return;};let Some(screen)=window.screen() else {return;};
                let rect=window.convertRectToScreen(sender.convertRect_toView(sender.bounds(),None));
                let anchor=super::super::popover::Anchor::new(rect,screen.visibleFrame());
                let data=self.ivars();
                let modules=data.modules.borrow().clone();
                if modules.is_empty() {return;}
                let selected=*data.selected.borrow();
                let view=MonitorPopoverView {modules,selected,combined:data.combined};
                if let Err(error)=super::super::popover::click(&data.app,view,anchor) {log::warn!("Monitor popover failed: {}",error.code);}
            }
        }
    );
    struct Item {
        status: Retained<NSStatusItem>,
        _target: Retained<Target>,
        last: TextIcon,
    }
    struct ItemState {
        modules: Vec<MonitorModule>,
        selected: MonitorModule,
        combined: bool,
    }
    #[derive(Default)]
    struct Items {
        combined: Option<Item>,
        modules: BTreeMap<MonitorModule, Item>,
        rotation: usize,
        rotated_at: i64,
        rotation_modules: Vec<MonitorModule>,
    }
    thread_local! {static ITEMS:RefCell<Items>=RefCell::new(Items::default());}
    fn error() -> AppError {
        AppError::new("E_MONITOR_UNAVAILABLE", "menubar_render")
    }
    /// Clear space either side of centered content. AppKit already spaces menu bar items apart.
    const PAD: f64 = 2.0;
    /// Graphs take x 2–42 and the current value starts here.
    const GRAPH: f64 = 45.0;
    /// One metric at a time in the combined item; five seconds is long enough to read without lingering.
    const ROTATE_EVERY_MS: i64 = 5_000;
    type Attrs = NSDictionary<NSString, objc2::runtime::AnyObject>;
    struct Layout {
        /// Font and baseline of each line, top first. The last font also measures `widest`.
        fonts: Vec<(Retained<NSFont>, f64)>,
        left: f64,
        min_width: f64,
        pad: f64,
        centered: bool,
    }
    /// Label and value follow Stats' mini widget: a 7 pt label over a 12 pt value, left-aligned in
    /// 31 pt, wider only when `widest` needs it. The value alone keeps that font and width, centered,
    /// so it never takes more room than the two lines.
    fn layout(text: &TextIcon) -> Layout {
        let (light, regular, medium) =
            unsafe { (NSFontWeightLight, NSFontWeightRegular, NSFontWeightMedium) };
        let system = NSFont::systemFontOfSize_weight;
        let tabular = NSFont::monospacedDigitSystemFontOfSize_weight;
        if text.combined {
            Layout {
                fonts: vec![(system(12.0, regular), 6.5)],
                left: 2.0,
                min_width: 0.0,
                pad: 2.0,
                centered: false,
            }
        } else if text.graph.is_some() {
            Layout {
                fonts: vec![(tabular(12.0, medium), 6.5)],
                left: GRAPH,
                min_width: 0.0,
                pad: PAD,
                centered: true,
            }
        } else if text.module == MonitorModule::Sensors {
            // Stats' stack widget: one 13 pt reading from x 2, with 4 pt after it.
            Layout {
                fonts: vec![(system(13.0, regular), 6.5)],
                left: 2.0,
                min_width: 0.0,
                pad: 4.0,
                centered: false,
            }
        } else if text.lines.len() == 1 {
            Layout {
                fonts: vec![(system(12.0, regular), 6.5)],
                left: 0.0,
                min_width: 31.0,
                pad: 0.0,
                centered: true,
            }
        } else if text.module == MonitorModule::Network {
            Layout {
                fonts: vec![(tabular(9.0, medium), 13.0), (tabular(9.0, medium), 3.0)],
                left: PAD,
                min_width: 0.0,
                pad: PAD,
                centered: true,
            }
        } else {
            Layout {
                fonts: vec![(system(7.0, light), 14.0), (system(12.0, regular), 3.0)],
                left: 0.0,
                min_width: 31.0,
                pad: 0.0,
                centered: false,
            }
        }
    }
    fn attrs(font: &NSFont) -> Retained<Attrs> {
        let color = NSColor::blackColor();
        let keys = unsafe { [NSFontAttributeName, NSForegroundColorAttributeName] };
        Attrs::from_slices(&keys, &[font.as_ref(), color.as_ref()])
    }
    fn measure(line: &NSString, attrs: &Attrs) -> f64 {
        unsafe { line.sizeWithAttributes(Some(attrs)) }.width
    }
    /// Draw both 1x and 2x alpha templates in memory. No screenshots, image files or metrics reach disk.
    pub fn render(text: &TextIcon, mtm: MainThreadMarker) -> Result<Retained<NSImage>, AppError> {
        let layout = layout(text);
        let rows: Vec<_> = text
            .lines
            .iter()
            .zip(&layout.fonts)
            .map(|(line, (font, baseline))| {
                let line = NSString::from_str(line);
                let attrs = attrs(font);
                let width = measure(&line, &attrs);
                (line, attrs, *baseline, width)
            })
            .collect();
        // Size for the widest reading the module can show, not the current one.
        let widest = rows.last().map_or(0.0, |(_, attrs, ..)| {
            measure(&NSString::from_str(&text.widest), attrs)
        });
        let column = rows.iter().map(|row| row.3).fold(widest, f64::max);
        let width = (layout.left + column + layout.pad)
            .max(layout.min_width)
            .ceil();
        let inner = width - layout.left - layout.pad;
        let image = NSImage::initWithSize(NSImage::alloc(), NSSize::new(width, 22.0));
        for scale in [1.0, 2.0] {
            let rep=unsafe {NSBitmapImageRep::initWithBitmapDataPlanes_pixelsWide_pixelsHigh_bitsPerSample_samplesPerPixel_hasAlpha_isPlanar_colorSpaceName_bytesPerRow_bitsPerPixel(NSBitmapImageRep::alloc(),std::ptr::null_mut(),(width*scale) as isize,(22.0*scale) as isize,8,4,true,false,NSDeviceRGBColorSpace,0,0)}.ok_or_else(error)?;
            let context =
                NSGraphicsContext::graphicsContextWithBitmapImageRep(&rep).ok_or_else(error)?;
            NSGraphicsContext::saveGraphicsState_class();
            NSGraphicsContext::setCurrentContext(Some(&context));
            NSColor::clearColor().setFill();
            NSRectFillUsingOperation(
                NSRect::new(
                    NSPoint::new(0.0, 0.0),
                    NSSize::new(width * scale, 22.0 * scale),
                ),
                NSCompositingOperation::Copy,
            );
            let transform = NSAffineTransform::new();
            transform.scaleXBy_yBy(scale, scale);
            transform.concat();
            for (line, attrs, baseline, line_width) in &rows {
                let x = if layout.centered {
                    layout.left + (inner - line_width) / 2.0
                } else {
                    layout.left
                };
                // Without UsesLineFragmentOrigin the rect origin is the baseline, as in Stats.
                unsafe {
                    line.drawWithRect_options_attributes_context(
                        NSRect::new(NSPoint::new(x, *baseline), NSSize::new(*line_width, 0.0)),
                        NSStringDrawingOptions::empty(),
                        Some(attrs),
                        None,
                    );
                }
            }
            if let Some(graph) = &text.graph {
                NSColor::blackColor().setStroke();
                NSColor::blackColor().setFill();
                match graph {
                    Graph::Line(values) => {
                        let path = NSBezierPath::bezierPath();
                        path.setLineWidth(1.2);
                        let mut connected = false;
                        for (i, value) in values.iter().enumerate() {
                            if let Some(v) = value {
                                let point = NSPoint::new(
                                    2.0 + i as f64 * 39.0
                                        / values.len().saturating_sub(1).max(1) as f64,
                                    3.0 + v * 16.0,
                                );
                                if connected {
                                    path.lineToPoint(point);
                                } else {
                                    path.moveToPoint(point);
                                    connected = true;
                                }
                            } else {
                                connected = false;
                            }
                        }
                        path.stroke();
                    }
                    Graph::Bars(values) => {
                        let step = 40.0 / values.len().max(1) as f64;
                        for (i, v) in values.iter().enumerate() {
                            let path = NSBezierPath::bezierPathWithRect(NSRect::new(
                                NSPoint::new(2.0 + i as f64 * step, 3.0),
                                NSSize::new((step - 1.0).max(0.3), v * 16.0),
                            ));
                            path.fill();
                        }
                    }
                }
            }
            NSGraphicsContext::restoreGraphicsState_class();
            // Keep the context in pixel coordinates until drawing is complete. Setting
            // the logical size earlier would add AppKit's scale on top of our transform.
            rep.setSize(NSSize::new(width, 22.0));
            image.addRepresentation(&rep);
        }
        image.setTemplate(true);
        let _ = mtm;
        Ok(image)
    }
    fn update(item: &mut Item, text: TextIcon, mtm: MainThreadMarker) -> Result<(), AppError> {
        if item.last == text {
            return Ok(());
        }
        let image = render(&text, mtm)?;
        if let Some(button) = item.status.button(mtm) {
            button.setImage(Some(&image));
        }
        item.status.setLength(image.size().width);
        item.last = text;
        Ok(())
    }
    fn create(
        app: &tauri::AppHandle,
        mtm: MainThreadMarker,
        text: TextIcon,
        state: ItemState,
        autosave: &str,
        tooltip: &str,
    ) -> Result<Item, AppError> {
        let image = render(&text, mtm)?;
        let status = NSStatusBar::systemStatusBar().statusItemWithLength(image.size().width);
        status.setAutosaveName(Some(&NSString::from_str(autosave)));
        let target = Target::alloc(mtm).set_ivars(TargetData {
            app: app.clone(),
            modules: RefCell::new(state.modules),
            selected: RefCell::new(state.selected),
            combined: state.combined,
        });
        let target: Retained<Target> = unsafe { objc2::msg_send![super(target), init] };
        if let Some(button) = status.button(mtm) {
            button.setImage(Some(&image));
            button.setToolTip(Some(&NSString::from_str(tooltip)));
            unsafe {
                button.setTarget(Some(&target));
                button.setAction(Some(sel!(clicked:)));
            }
        }
        Ok(Item {
            status,
            _target: target,
            last: text,
        })
    }
    fn remove(item: &Item) {
        NSStatusBar::systemStatusBar().removeStatusItem(&item.status);
    }
    pub fn refresh(app: &tauri::AppHandle, p: &Publication) -> Result<(), AppError> {
        let mtm = MainThreadMarker::new().ok_or_else(error)?;
        objc2::rc::autoreleasepool(|_| {
            ITEMS.with(|items| {
                let mut items = items.borrow_mut();
                let shown: Vec<_> = p
                    .menu
                    .enabled()
                    .intersection(&p.available)
                    .copied()
                    .collect();
                if p.menu.combined {
                    for (_, item) in std::mem::take(&mut items.modules) {
                        remove(&item);
                    }
                    if shown.is_empty() {
                        items.rotation = 0;
                        items.rotated_at = p.sample.at;
                        items.rotation_modules.clear();
                        if let Some(item) = items.combined.take() {
                            remove(&item);
                        }
                    } else {
                        if items.rotation_modules != shown {
                            items.rotation_modules = shown.clone();
                            items.rotation = 0;
                            items.rotated_at = p.sample.at;
                        } else if p.sample.at < items.rotated_at {
                            items.rotated_at = p.sample.at;
                        } else if p.sample.at.saturating_sub(items.rotated_at) >= ROTATE_EVERY_MS {
                            items.rotation = (items.rotation + 1) % shown.len();
                            items.rotated_at = p.sample.at;
                        }
                        let text = combined_text(&p.menu, &shown, &p.sample, items.rotation);
                        let selected = shown[items.rotation];
                        if let Some(item) = &mut items.combined {
                            *item._target.ivars().modules.borrow_mut() = shown.clone();
                            *item._target.ivars().selected.borrow_mut() = selected;
                            update(item, text, mtm)?;
                        } else {
                            items.combined = Some(create(
                                app,
                                mtm,
                                text,
                                ItemState {
                                    modules: shown,
                                    selected,
                                    combined: true,
                                },
                                "monitor.combined",
                                "OpenNavo · System Monitor",
                            )?);
                        }
                    }
                    return Ok(());
                }
                if let Some(item) = items.combined.take() {
                    remove(&item);
                }
                items.rotation = 0;
                items.rotated_at = 0;
                items.rotation_modules.clear();
                items.modules.retain(|module, item| {
                    let keep = shown.contains(module);
                    if !keep {
                        remove(item);
                    }
                    keep
                });
                for module in shown {
                    let text = icon(module, p.menu.item(module).style, &p.sample, &p.history);
                    if let Some(item) = items.modules.get_mut(&module) {
                        update(item, text, mtm)?;
                    } else {
                        let item = create(
                            app,
                            mtm,
                            text,
                            ItemState {
                                modules: vec![module],
                                selected: module,
                                combined: false,
                            },
                            &format!("monitor.{}", module.key()),
                            &format!("OpenNavo · {}", module.key()),
                        )?;
                        items.modules.insert(module, item);
                    }
                }
                Ok(())
            })
        })
    }
    pub fn statuses(menu: &MonitorMenuBar) -> Vec<MenuBarItemStatus> {
        ITEMS.with(|items| {
            let items = items.borrow();
            menu.enabled()
                .into_iter()
                .map(|module| {
                    let installed = if menu.combined {
                        items.combined.is_some()
                    } else {
                        items.modules.contains_key(&module)
                    };
                    MenuBarItemStatus {
                        module,
                        installed,
                        visible: None,
                    }
                })
                .collect()
        })
    }
    /// Also works for global events, where AppKit supplies no local status window.
    pub fn contains_point(point: NSPoint, mtm: MainThreadMarker) -> bool {
        ITEMS.with(|items| {
            let items = items.borrow();
            items
                .combined
                .iter()
                .chain(items.modules.values())
                .any(|item| {
                    item.status
                        .button(mtm)
                        .is_some_and(|button| crate::tray::panel::contains_button(&button, point))
                })
        })
    }
}
#[cfg(target_os = "macos")]
pub use mac::{contains_point, render as render_template};
pub fn refresh(app: &tauri::AppHandle, p: Publication) {
    if !p.menu_due {
        return;
    }
    #[cfg(target_os = "macos")]
    {
        let app_handle = app.clone();
        let _ = app.run_on_main_thread(move || {
            if let Err(error) = mac::refresh(&app_handle, &p) {
                log::warn!("Monitor menu refresh failed: {}", error.code);
            }
        });
    }
    #[cfg(not(target_os = "macos"))]
    {
        let _ = (app, p);
    }
}
pub async fn status(
    app: tauri::AppHandle,
    menu: MonitorMenuBar,
) -> Result<Vec<MenuBarItemStatus>, AppError> {
    let (send, receive) = tokio::sync::oneshot::channel();
    app.run_on_main_thread(move || {
        #[cfg(target_os = "macos")]
        let result = mac::statuses(&menu);
        #[cfg(not(target_os = "macos"))]
        let result = menu
            .enabled()
            .into_iter()
            .map(|module| MenuBarItemStatus {
                module,
                installed: false,
                visible: None,
            })
            .collect();
        let _ = send.send(result);
    })
    .map_err(|_| AppError::new("E_UNKNOWN", "monitor_main_thread"))?;
    receive
        .await
        .map_err(|_| AppError::new("E_UNKNOWN", "monitor_main_thread"))
}
