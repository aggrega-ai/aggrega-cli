// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! The rules-document form of an attached policy, and how it is read.
//!
//! A policy can be attached in either of two forms, and the file's name says
//! which. A file named `.cel` is one expression, evaluated as it stands. A file
//! named `.yaml` or `.yml` is a **rules document**: a list of rules, each naming
//! the decision key it reaches, the expression that decides whether it is
//! reached, and the message reported when it is.
//!
//! ```yaml
//! - key: deny
//!   expression: size(input.graph.spec.nodes) > 20
//!   message: this build declares a lot of components
//! - key: warn
//!   expression: has(input.settings.max_nodes)
//!   message: a component ceiling is configured for this environment
//! ```
//!
//! # Which form to write
//!
//! A rules document suits a rule whose message is fixed: the message is written
//! once, beside the expression that reaches it, and reads as a list of guards. An
//! expression suits a rule whose message names what it found — one expression can
//! filter the components of a build and produce a message per component, which a
//! rules document cannot, because each of its rules carries a single message
//! written in advance. So the two forms are not interchangeable: the document form
//! is the shorter way to write a fixed message, and the expression form is what
//! reports one message per thing found.
//!
//! # A rules document is read as the expression it stands for
//!
//! There is one evaluator, and it evaluates expressions. So a rules document is
//! translated into the equivalent expression before anything is evaluated, and
//! that expression is compiled and evaluated by the same path a hand-written
//! expression takes. Each rule becomes its decision key carrying the message when
//! the expression holds and nothing when it does not, and rules sharing a key are
//! joined:
//!
//! ```cel
//! {
//!   "deny": ((size(input.graph.spec.nodes) > 20
//! ) ? ["this build declares a lot of components"] : []),
//! }
//! ```
//!
//! The closing parenthesis sits on a line of its own because the expression
//! language has line comments, so an expression may legally end in one.
//!
//! Two consequences follow from there being one evaluator rather than two, and
//! both are properties of the product rather than accidents:
//!
//! - A rules document and the expression it stands for produce identical
//!   decisions, because they are the same evaluation.
//! - The messages under one decision key are reported sorted, which is the
//!   ordering the evaluated form documents. So a rules document's messages are
//!   reported in sorted order rather than in the order the rules were written.
//!
//! # A malformed rules document is refused, by name
//!
//! Every way a rules document can be wrong is reported as a build fault naming
//! the file and, where the fault belongs to one rule, that rule's position and
//! decision key: text that is not a list of rules, a list with no rules in it, a
//! rule missing a field or carrying an empty one, a field the form does not
//! define, and an expression that cannot be read. A rule whose expression cannot
//! be read is refused here rather than at evaluation, so the failure names the
//! rule the author wrote instead of a position inside translated text they never
//! saw.

use serde::Deserialize;

/// The file-name suffixes that mark an attached file as a rules document.
///
/// The name decides the form, and nothing else does: an attachment does not
/// declare which form its files are in, and the text is not examined to guess.
/// Reading the form off the name keeps a policy set self-describing, and the text
/// could not answer it anyway: an expression's own outermost value is a map, and
/// so is a document's, so the two forms are indistinguishable by shape.
const RULES_DOCUMENT_SUFFIXES: [&str; 2] = [".yaml", ".yml"];

/// The decision key a rule reaches, its condition, and the message reported.
///
/// Every field is required and none may be empty. A rule with no message reports
/// nothing when it fires, which would make it a rule that cannot be observed; a
/// rule with no expression has no condition to decide on; a rule with no decision
/// key reaches nothing. Each of those is an unfinished rule rather than a rule
/// that allows, so each is refused.
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct Rule {
    /// The decision key this rule reaches, which the attachment's translation
    /// maps to a severity.
    key: String,
    /// The condition, written in the same expression language a `.cel` file
    /// carries. It decides whether the rule is reached; it does not produce the
    /// message.
    expression: String,
    /// What is reported when the expression holds, exactly as written.
    message: String,
}

/// Reports whether an attached file's name marks it as a rules document.
///
/// A file whose name carries neither rules-document suffix is one expression,
/// which is what an attachment carrying files named for no particular form gets.
pub fn is_rules_document(path: &str) -> bool {
    RULES_DOCUMENT_SUFFIXES
        .iter()
        .any(|suffix| path.ends_with(suffix))
}

/// Reads a rules document and returns the expression it stands for.
///
/// `path` is the attached file's name and appears in every failure message, so an
/// operator reading one knows which attachment to look at.
///
/// The returned text is compiled and evaluated by the same path a hand-written
/// expression takes. Nothing here evaluates anything or reads a decision: this
/// produces text, and the decisions come from evaluating it.
pub fn expression_source(path: &str, content: &str) -> anyhow::Result<String> {
    let rules: Vec<Rule> = serde_yaml::from_str(content).map_err(|err| {
        anyhow::anyhow!(
            "rules document {path:?} could not be read: a rules document is a list of rules, each \
             naming the decision key it reaches, the expression that decides whether it is \
             reached, and the message reported when it is. {err}"
        )
    })?;
    if rules.is_empty() {
        anyhow::bail!(
            "rules document {path:?} declares no rules, so it decides nothing. A rules document \
             is a list of rules, each naming a decision key, an expression and a message."
        );
    }
    for (index, rule) in rules.iter().enumerate() {
        // Rules are located from one, the way an author counts them reading their
        // own file.
        check(path, index + 1, rule)?;
    }
    Ok(expression(&rules))
}

/// Requires one rule to be complete and its expression readable.
///
/// The expression is compiled and the compiled form discarded. Compiling it here
/// costs one compilation and buys the whole point of the check: the failure names
/// the rule and the decision key the author wrote, where the same failure raised
/// against the translated text would locate a position in text the author never
/// saw. It reads no decision and produces no verdict.
fn check(path: &str, position: usize, rule: &Rule) -> anyhow::Result<()> {
    for (field, value) in [
        ("key", &rule.key),
        ("expression", &rule.expression),
        ("message", &rule.message),
    ] {
        if value.trim().is_empty() {
            anyhow::bail!(
                "rule {position} of rules document {path:?} leaves {field} empty; a rule names the \
                 decision key it reaches, the expression that decides whether it is reached, and \
                 the message reported when it is."
            );
        }
    }
    cel::Program::compile(&rule.expression).map_err(|err| {
        anyhow::anyhow!(
            "rule {position} of rules document {path:?}, under decision key {key:?}, has an \
             expression that cannot be read: {err}",
            key = rule.key
        )
    })?;
    Ok(())
}

/// Renders the rules as the expression they stand for.
///
/// Rules are grouped by decision key in the order the keys first appear, and the
/// rules under one key keep the order they were written in. Nothing here iterates
/// a map, so identical text yields identical text.
fn expression(rules: &[Rule]) -> String {
    let mut source = String::from("{\n");
    for key in keys(rules) {
        source.push_str("  ");
        source.push_str(&quoted(key));
        source.push_str(": ");
        let mut joined = false;
        for rule in rules.iter().filter(|rule| rule.key == key) {
            if joined {
                source.push_str(" + ");
            }
            joined = true;
            source.push_str(&reached(rule));
        }
        source.push_str(",\n");
    }
    source.push('}');
    source
}

/// The decision keys the rules name, in the order they first appear.
fn keys(rules: &[Rule]) -> Vec<&str> {
    let mut keys: Vec<&str> = Vec::new();
    for rule in rules {
        if !keys.contains(&rule.key.as_str()) {
            keys.push(rule.key.as_str());
        }
    }
    keys
}

/// Renders one rule as the messages it contributes to its decision key: the
/// message when the expression holds, nothing when it does not.
///
/// The expression is parenthesised so its own operators cannot reach outside it,
/// and it is closed on a line of its own because the expression language has line
/// comments: an expression ending in one would otherwise swallow whatever
/// followed it on the same line.
fn reached(rule: &Rule) -> String {
    let condition = &rule.expression;
    let message = quoted(&rule.message);
    format!("(({condition}\n) ? [{message}] : [])")
}

/// Renders text as a string literal the expression language reads back as exactly
/// that text.
///
/// A message is an operator's own words, so it may hold anything text can hold —
/// a quotation mark, a backslash, a line break. Each of those either ends the
/// literal early or changes what it decodes to, so each is written as the escape
/// the language decodes back to it. A line break is the one that must be escaped
/// rather than merely ought to be: a literal break inside a quoted literal is not
/// readable text in this language.
fn quoted(text: &str) -> String {
    let mut literal = String::with_capacity(text.len() + 2);
    literal.push('"');
    for character in text.chars() {
        match character {
            '\\' => literal.push_str("\\\\"),
            '"' => literal.push_str("\\\""),
            '\n' => literal.push_str("\\n"),
            '\r' => literal.push_str("\\r"),
            '\t' => literal.push_str("\\t"),
            // Every other control character is written as its code point. None of
            // them ends a literal, so this is legibility of the rendered text
            // rather than correctness of it.
            other if other.is_control() => {
                literal.push_str(&format!("\\u{:04x}", other as u32));
            }
            other => literal.push(other),
        }
    }
    literal.push('"');
    literal
}

#[cfg(test)]
mod tests {
    use super::*;

    /// Evaluates a standalone expression and returns what it produced, so a test
    /// asserts on translated text through what the evaluator makes of it rather
    /// than through its spelling.
    fn evaluated(expression: &str) -> cel::Value {
        let program = cel::Program::compile(expression)
            .unwrap_or_else(|err| panic!("the translated text compiles: {err}\n{expression}"));
        program
            .execute(&cel::Context::default())
            .unwrap_or_else(|err| panic!("the translated text evaluates: {err}\n{expression}"))
    }

    /// The messages one decision key collects out of an evaluated expression.
    fn collected(value: &cel::Value, key: &str) -> Vec<String> {
        let cel::Value::Map(map) = value else {
            panic!("the translated text evaluates to a map, not {:?}", value);
        };
        let Some(cel::Value::List(items)) = map.get(&cel::objects::KeyRef::String(key)) else {
            panic!("decision key {key:?} is absent from {value:?}");
        };
        items
            .iter()
            .map(|item| match item {
                cel::Value::String(text) => text.to_string(),
                other => panic!("a message is text, not {other:?}"),
            })
            .collect()
    }

    #[test]
    fn a_named_rules_document_is_recognised_and_others_are_not() {
        assert!(is_rules_document("policy.yaml"));
        assert!(is_rules_document("policy.yml"));
        assert!(is_rules_document("rules/limits.yaml"));
        assert!(!is_rules_document("policy.cel"));
        assert!(!is_rules_document("policy"));
        assert!(!is_rules_document("policy.yaml.cel"));
    }

    #[test]
    fn one_rule_becomes_its_key_carrying_its_message_when_the_expression_holds() {
        let source = expression_source(
            "policy.yaml",
            "- key: deny\n  expression: size(input) > 1\n  message: too many\n",
        )
        .expect("a well-formed rules document is read");
        assert_eq!(
            source,
            "{\n  \"deny\": ((size(input) > 1\n) ? [\"too many\"] : []),\n}"
        );
    }

    #[test]
    fn a_rule_contributes_its_message_only_when_its_expression_holds() {
        let held = expression_source(
            "policy.yaml",
            "- key: deny\n  expression: \"true\"\n  message: reached\n",
        )
        .expect("a well-formed rules document is read");
        assert_eq!(collected(&evaluated(&held), "deny"), vec!["reached"]);

        let unheld = expression_source(
            "policy.yaml",
            "- key: deny\n  expression: \"false\"\n  message: reached\n",
        )
        .expect("a well-formed rules document is read");
        assert!(collected(&evaluated(&unheld), "deny").is_empty());
    }

    #[test]
    fn rules_sharing_a_decision_key_are_joined_under_it() {
        let source = expression_source(
            "policy.yaml",
            "- key: deny\n  expression: \"true\"\n  message: first\n\
             - key: deny\n  expression: \"false\"\n  message: second\n\
             - key: deny\n  expression: \"true\"\n  message: third\n",
        )
        .expect("a well-formed rules document is read");
        assert_eq!(
            collected(&evaluated(&source), "deny"),
            vec!["first", "third"]
        );
    }

    #[test]
    fn decision_keys_appear_in_the_order_they_are_first_named() {
        // The translated text is an input to compilation, so it is generated
        // deterministically rather than by walking a map.
        let source = expression_source(
            "policy.yaml",
            "- key: warn\n  expression: \"true\"\n  message: a\n\
             - key: deny\n  expression: \"true\"\n  message: b\n\
             - key: warn\n  expression: \"true\"\n  message: c\n",
        )
        .expect("a well-formed rules document is read");
        let warn = source.find("\"warn\"").expect("the first key appears");
        let deny = source.find("\"deny\"").expect("the second key appears");
        assert!(warn < deny, "{source}");
        assert_eq!(collected(&evaluated(&source), "warn"), vec!["a", "c"]);
        assert_eq!(collected(&evaluated(&source), "deny"), vec!["b"]);
    }

    #[test]
    fn translating_the_same_document_twice_yields_the_same_text() {
        let document = "- key: warn\n  expression: \"true\"\n  message: a\n\
                        - key: deny\n  expression: \"true\"\n  message: b\n";
        let first = expression_source("policy.yaml", document).expect("read");
        let second = expression_source("policy.yaml", document).expect("read");
        assert_eq!(first, second);
    }

    #[test]
    fn a_document_that_is_not_a_list_of_rules_is_refused_by_name() {
        let err = expression_source("policy.yaml", "deny: everything\n")
            .expect_err("a mapping is not a list of rules");
        let text = err.to_string();
        assert!(text.contains("policy.yaml"), "{text}");
        assert!(text.contains("list of rules"), "{text}");
    }

    #[test]
    fn a_document_with_no_rules_is_refused_by_name() {
        for content in ["[]\n", "", "\n"] {
            let err = expression_source("policy.yaml", content)
                .expect_err("a document with no rules decides nothing");
            let text = err.to_string();
            assert!(text.contains("policy.yaml"), "{content:?} -> {text}");
        }
        let err = expression_source("policy.yaml", "[]\n").expect_err("an empty list is refused");
        assert!(err.to_string().contains("declares no rules"), "{err}");
    }

    #[test]
    fn a_rule_missing_a_field_is_refused_by_name() {
        let err = expression_source("policy.yaml", "- key: deny\n  message: no expression\n")
            .expect_err("a rule needs an expression");
        let text = err.to_string();
        assert!(text.contains("policy.yaml"), "{text}");
        assert!(text.contains("expression"), "{text}");
    }

    #[test]
    fn a_field_the_form_does_not_define_is_refused_rather_than_ignored() {
        // A misspelled field name is the mistake this catches: silently ignoring
        // it would leave the author with a rule that reads as configured and
        // decides something else.
        let err = expression_source(
            "policy.yaml",
            "- key: deny\n  expression: \"true\"\n  message: m\n  severity: halt\n",
        )
        .expect_err("an undefined field is refused");
        let text = err.to_string();
        assert!(text.contains("policy.yaml"), "{text}");
        assert!(text.contains("severity"), "{text}");
    }

    #[test]
    fn an_empty_field_is_refused_and_the_rule_is_located() {
        let cases = [
            ("key", "- key: \"\"\n  expression: \"true\"\n  message: m\n"),
            (
                "expression",
                "- key: deny\n  expression: \"  \"\n  message: m\n",
            ),
            (
                "message",
                "- key: deny\n  expression: \"true\"\n  message: \"\"\n",
            ),
        ];
        for (field, content) in cases {
            let document =
                format!("- key: warn\n  expression: \"true\"\n  message: fine\n{content}");
            let text = match expression_source("policy.yaml", &document) {
                Ok(translated) => panic!("{field}: expected a refusal, got {translated}"),
                Err(err) => err.to_string(),
            };
            assert!(text.contains("policy.yaml"), "{field}: {text}");
            assert!(text.contains(field), "{field}: {text}");
            assert!(
                text.contains("rule 2"),
                "the rule is located: {field}: {text}"
            );
        }
    }

    #[test]
    fn an_expression_that_cannot_be_read_is_refused_against_the_rule_that_wrote_it() {
        // The whole point of checking each expression on its own: the author is
        // pointed at their own rule rather than at a position inside translated
        // text they never saw.
        let err = expression_source(
            "policy.yaml",
            "- key: warn\n  expression: \"true\"\n  message: fine\n\
             - key: deny\n  expression: \"size(input) >\"\n  message: broken\n",
        )
        .expect_err("an unreadable expression is refused");
        let text = err.to_string();
        assert!(text.contains("policy.yaml"), "{text}");
        assert!(text.contains("rule 2"), "{text}");
        assert!(text.contains("\"deny\""), "{text}");
        assert!(text.contains("cannot be read"), "{text}");
    }

    #[test]
    fn a_message_holding_a_quote_a_backslash_and_a_line_break_reads_back_exactly() {
        // A message is an operator's own words, and it is written into the text
        // that gets compiled. Each of these either ends the literal early or
        // changes what it decodes to, so this is the case the escaping exists for
        // and the one whose failure would land on the operator's own wording.
        let authored = "a \"quoted\" word, a back\\slash, a\nline break, and a\ttab";
        let document = "- key: deny\n  expression: \"true\"\n  \
                        message: \"a \\\"quoted\\\" word, a back\\\\slash, a\\nline break, and a\\ttab\"\n";
        let source = expression_source("policy.yaml", document).expect("the document is read");
        assert_eq!(collected(&evaluated(&source), "deny"), vec![authored]);
    }

    #[test]
    fn an_expression_ending_in_a_comment_still_reads() {
        // The expression language has line comments, so an expression may legally
        // end in one. Closing the expression on the same line would put the rest
        // of the translated text inside that comment.
        let source = expression_source(
            "policy.yaml",
            "- key: deny\n  expression: \"true // a note\"\n  message: reached\n",
        )
        .expect("an expression ending in a comment is readable");
        assert_eq!(collected(&evaluated(&source), "deny"), vec!["reached"]);
    }

    #[test]
    fn a_message_reads_back_exactly_across_every_kind_of_character() {
        // Breadth rather than a chosen example: every code point below the point
        // where the escaping rules stop varying, plus characters outside the basic
        // plane, each carried through translation, compilation and evaluation.
        let mut checked = 0usize;
        let ranges = [0x00u32..=0x2FFu32, 0x2000..=0x206F, 0x1F400..=0x1F42F];
        for range in ranges {
            for code in range {
                let Some(character) = char::from_u32(code) else {
                    continue;
                };
                let authored = format!("x{character}y");
                let literal = quoted(&authored);
                let expression = format!("{{\"deny\": [{literal}]}}");
                assert_eq!(
                    collected(&evaluated(&expression), "deny"),
                    vec![authored.clone()],
                    "code point {code:#06x} did not read back: {literal}"
                );
                checked += 1;
            }
        }
        // The exact count is asserted rather than a floor, so a range edited by
        // accident is a failure rather than a quietly narrower check.
        assert_eq!(
            checked, 928,
            "the breadth check covered {checked} characters"
        );
    }
}
