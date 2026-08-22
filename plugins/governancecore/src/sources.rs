// SPDX-License-Identifier: Apache-2.0
// Copyright 2026 Cognitio S.r.l.

//! One attached file, as any policy engine reads it.
//!
//! Each governance role receives its own source type on the wire, all with the
//! same two fields. This borrowed view is what lets one evaluator serve every
//! role without copying content or restating the loading rules per role — and,
//! because it names no policy engine, what lets every engine share the one
//! projection instead of each carrying its own.

use aggrega_abi::{AnnotationSource, MutatorSource, PolicySource};

/// One attached file as an evaluator reads it: a name and its text.
#[derive(Debug, Clone, Copy)]
pub struct Source<'a> {
    /// The attached file's name.
    pub path: &'a str,
    /// The file's text, exactly as it was attached.
    pub content: &'a str,
}

impl<'a> From<&'a PolicySource> for Source<'a> {
    fn from(source: &'a PolicySource) -> Self {
        Source {
            path: source.path.as_str(),
            content: source.content.as_str(),
        }
    }
}

impl<'a> From<&'a MutatorSource> for Source<'a> {
    fn from(source: &'a MutatorSource) -> Self {
        Source {
            path: source.path.as_str(),
            content: source.content.as_str(),
        }
    }
}

impl<'a> From<&'a AnnotationSource> for Source<'a> {
    fn from(source: &'a AnnotationSource) -> Self {
        Source {
            path: source.path.as_str(),
            content: source.content.as_str(),
        }
    }
}

/// Projects a role's own source entries into the view an evaluator reads.
///
/// Each role carries its own source type with the same two fields, so this is
/// the one place the four payload shapes converge on a single reader — the
/// alternative would be one copy of the loading rules per role, per engine.
pub fn project<'a, S>(entries: &'a [S]) -> Vec<Source<'a>>
where
    Source<'a>: From<&'a S>,
{
    entries.iter().map(Source::from).collect()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn each_role_source_type_projects_onto_the_shared_view() {
        let policy = PolicySource {
            path: "a".to_string(),
            content: "x".to_string(),
        };
        let mutator = MutatorSource {
            path: "b".to_string(),
            content: "y".to_string(),
        };
        let annotation = AnnotationSource {
            path: "c".to_string(),
            content: "z".to_string(),
        };
        assert_eq!(Source::from(&policy).path, "a");
        assert_eq!(Source::from(&mutator).path, "b");
        assert_eq!(Source::from(&annotation).path, "c");
    }

    #[test]
    fn projecting_a_list_keeps_its_order() {
        let entries = vec![
            PolicySource {
                path: "first".to_string(),
                content: String::new(),
            },
            PolicySource {
                path: "second".to_string(),
                content: String::new(),
            },
        ];
        let projected = project(&entries);
        let paths: Vec<&str> = projected.iter().map(|s| s.path).collect();
        assert_eq!(paths, vec!["first", "second"]);
    }
}
