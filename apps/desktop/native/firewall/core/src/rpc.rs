// SPDX-License-Identifier: GPL-3.0-only
use crate::{engine::*, types::*};
use serde::de::DeserializeOwned;
use serde_json::{Value, json};

fn decode<T: DeserializeOwned>(value: &Value, key: &str) -> Result<T, FirewallErrorCode> {
    serde_json::from_value(value[key].clone()).map_err(|_| FirewallErrorCode::RuleInvalid)
}
fn string<'a>(value: &'a Value, key: &str) -> Result<&'a str, FirewallErrorCode> {
    value[key].as_str().ok_or(FirewallErrorCode::RuleInvalid)
}
impl Engine {
    pub fn request(&mut self, request: Value) -> Result<Value, FirewallErrorCode> {
        let at = request["at"].as_i64().unwrap_or_default();
        if let Some(day) = request["dayStart"].as_i64() {
            self.set_day(day);
        }
        if let Some(now) = request["now"].as_u64() {
            self.expire(now, at);
        }
        match string(&request, "op")? {
            "suspend" => {
                self.suspend(at);
                Ok(Value::Null)
            }
            "resume" => {
                self.resume();
                Ok(Value::Null)
            }
            "cancel" => {
                self.cancel(string(&request, "id")?, at);
                Ok(Value::Null)
            }
            "timeout" => {
                self.timeout(string(&request, "id")?, at);
                Ok(Value::Null)
            }
            "flow_details" => {
                self.flow_details(
                    string(&request, "id")?,
                    decode(&request, "args")?,
                    decode(&request, "ancestors")?,
                );
                Ok(Value::Null)
            }
            "bypass" => {
                let verdict = self.bypass(
                    decode(&request, "flow")?,
                    request["identified"].as_bool().unwrap_or(false),
                    request["released"].as_bool().unwrap_or(false),
                );
                Ok(json!({"verdict": verdict}))
            }
            "restore" => {
                *self = Self::restore(decode(&request, "state")?)?;
                Ok(Value::Null)
            }
            "persist" => Ok(json!(self.persistent())),
            "heartbeat" => {
                self.connect(
                    request["now"]
                        .as_u64()
                        .ok_or(FirewallErrorCode::Disconnected)?,
                );
                Ok(Value::Null)
            }
            "disconnect" => {
                self.disconnect(at);
                Ok(Value::Null)
            }
            "tick" => {
                self.tick(
                    request["now"]
                        .as_u64()
                        .ok_or(FirewallErrorCode::Disconnected)?,
                    at,
                    &decode(&request, "alive")?,
                );
                Ok(Value::Null)
            }
            "processes" => Ok(json!(self.processes())),
            "flow" => Ok(
                json!({"verdict":match self.flow(decode(&request,"flow")?) {Some(FirewallAction::Allow)=>"allow",Some(FirewallAction::Block)=>"block",None=>"pause"}}),
            ),
            "snapshot" => Ok(json!(self.snapshot())),
            "configure" => {
                let scan = self.configure(
                    string(&request, "id")?.to_owned(),
                    decode(&request, "settings")?,
                    // Older hosts omit it: keep what the extension already has.
                    request["fresh"].as_bool().unwrap_or(false),
                    at,
                );
                Ok(json!({"accepted": true, "scan": scan}))
            }
            "reset" => {
                self.reset();
                Ok(Value::Null)
            }
            "baseline_add" => {
                self.baseline_add(decode(&request, "apps")?, request["scan"].as_str(), at);
                Ok(Value::Null)
            }
            "baseline_finish" => {
                self.baseline_finish(request["scan"].as_str());
                Ok(Value::Null)
            }
            "rules" => Ok(json!(self.state.rules)),
            "rule_save" => {
                let input: FirewallRuleInput = decode(&request, "input")?;
                if input.app_key.is_some() && input.app_path.is_some() {
                    return Err(FirewallErrorCode::RuleInvalid);
                }
                let app = if input.app_path.is_some() {
                    Some(self.remember(decode(&request, "identifiedApp")?))
                } else if let Some(key) = &input.app_key {
                    Some(self.known_app(key).ok_or(FirewallErrorCode::RuleInvalid)?)
                } else {
                    None
                };
                Ok(json!(self.save_rule(input, app, at)?))
            }
            "rule_delete" => {
                self.delete_rules(&decode::<Vec<String>>(&request, "ids")?, at);
                Ok(Value::Null)
            }
            "alerts" => Ok(json!(self.alerts())),
            "alert_reply" => {
                self.reply(decode(&request, "reply")?, at)?;
                Ok(Value::Null)
            }
            "events" => {
                let query: FirewallEventsQuery = decode(&request, "query")?;
                let mut events: Vec<_> = self
                    .state
                    .events
                    .iter()
                    .filter(|e| {
                        query.before.is_none_or(|before| {
                            e.at < before
                                || (e.at == before
                                    && query.before_id.as_ref().is_some_and(|id| &e.id < id))
                        }) && (!query.blocked_only || e.action == FirewallAction::Block)
                    })
                    .collect();
                events.sort_by(|a, b| b.at.cmp(&a.at).then_with(|| b.id.cmp(&a.id)));
                events.truncate(query.limit.min(1000) as usize);
                Ok(json!(events))
            }
            "events_clear" => {
                self.state.events.clear();
                Ok(Value::Null)
            }
            "settings" => Ok(json!(self.state.settings)),
            "settings_set" => {
                self.set_settings(decode(&request, "settings")?, at);
                Ok(json!(self.state.settings))
            }
            "baseline" => {
                self.set_baseline(decode(&request, "apps")?);
                Ok(Value::Null)
            }
            "blocklists" => Ok(json!(
                self.state
                    .blocklists
                    .iter()
                    .map(|b| &b.info)
                    .collect::<Vec<_>>()
            )),
            "blocklist_load" => Ok(json!(self.blocklist_load(
                request["id"].as_str(),
                string(&request, "source")?.into(),
                string(&request, "name")?.into(),
                string(&request, "text")?.as_bytes(),
                at
            )?)),
            "blocklist_error" => {
                self.blocklist_error(string(&request, "id")?, decode(&request, "error")?)?;
                Ok(Value::Null)
            }
            "blocklist_set" => Ok(json!(self.blocklist_set(
                string(&request, "id")?,
                decode(&request, "enabled")?,
                at
            )?)),
            "blocklist_remove" => {
                self.blocklist_remove(string(&request, "id")?, at);
                Ok(Value::Null)
            }
            _ => Err(FirewallErrorCode::RuleInvalid),
        }
    }
}
