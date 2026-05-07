//! This module is responsible for parsing & validating a patch into a list of "hunks".
//! It does not attempt to check whether the patch can be applied to the filesystem.
//! The official Lark grammar is documented upstream and allows add/delete/update hunks.

use crate::ApplyPatchArgs;
use codex_utils_absolute_path::AbsolutePathBuf;
#[cfg(test)]
use codex_utils_absolute_path::test_support::PathBufExt;
use std::path::Path;
use std::path::PathBuf;
use thiserror::Error;

pub(crate) const BEGIN_PATCH_MARKER: &str = "*** Begin Patch";
pub(crate) const END_PATCH_MARKER: &str = "*** End Patch";
pub(crate) const ADD_FILE_MARKER: &str = "*** Add File: ";
pub(crate) const DELETE_FILE_MARKER: &str = "*** Delete File: ";
pub(crate) const UPDATE_FILE_MARKER: &str = "*** Update File: ";
pub(crate) const MOVE_TO_MARKER: &str = "*** Move to: ";
pub(crate) const EOF_MARKER: &str = "*** End of File";
pub(crate) const CHANGE_CONTEXT_MARKER: &str = "@@ ";
pub(crate) const EMPTY_CHANGE_CONTEXT_MARKER: &str = "@@";

const PARSE_IN_STRICT_MODE: bool = false;

#[derive(Debug, PartialEq, Error, Clone)]
pub enum ParseError {
    #[error("invalid patch: {0}")]
    InvalidPatchError(String),
    #[error("invalid hunk at line {line_number}, {message}")]
    InvalidHunkError { message: String, line_number: usize },
}

use ParseError::*;

#[derive(Debug, PartialEq, Clone)]
#[allow(clippy::enum_variant_names)]
pub enum Hunk {
    AddFile {
        path: PathBuf,
        contents: String,
    },
    DeleteFile {
        path: PathBuf,
    },
    UpdateFile {
        path: PathBuf,
        move_path: Option<PathBuf>,
        chunks: Vec<UpdateFileChunk>,
    },
}

impl Hunk {
    pub fn resolve_path(&self, cwd: &AbsolutePathBuf) -> AbsolutePathBuf {
        let path = match self {
            Hunk::UpdateFile { path, .. } => path,
            Hunk::AddFile { .. } | Hunk::DeleteFile { .. } => self.path(),
        };
        AbsolutePathBuf::resolve_path_against_base(path, cwd)
    }

    pub fn path(&self) -> &Path {
        match self {
            Hunk::AddFile { path, .. } => path,
            Hunk::DeleteFile { path } => path,
            Hunk::UpdateFile { move_path: Some(path), .. } => path,
            Hunk::UpdateFile {
                path,
                move_path: None,
                ..
            } => path,
        }
    }
}

use Hunk::*;

#[derive(Debug, PartialEq, Clone)]
pub struct UpdateFileChunk {
    pub change_context: Option<String>,
    pub old_lines: Vec<String>,
    pub new_lines: Vec<String>,
    pub is_end_of_file: bool,
}

pub fn parse_patch(patch: &str) -> Result<ApplyPatchArgs, ParseError> {
    let mode = if PARSE_IN_STRICT_MODE {
        ParseMode::Strict
    } else {
        ParseMode::Lenient
    };
    parse_patch_text(patch, mode)
}

enum ParseMode {
    Strict,
    Lenient,
}

fn parse_patch_text(patch: &str, mode: ParseMode) -> Result<ApplyPatchArgs, ParseError> {
    let lines: Vec<&str> = patch.trim().lines().collect();
    let (patch_lines, hunk_lines) = match mode {
        ParseMode::Strict => check_patch_boundaries_strict(&lines)?,
        ParseMode::Lenient => check_patch_boundaries_lenient(&lines)?,
    };
    let mut hunks: Vec<Hunk> = Vec::new();
    let mut remaining_lines = hunk_lines;
    let mut line_number = 2;
    while !remaining_lines.is_empty() {
        let (hunk, hunk_lines) = parse_one_hunk(remaining_lines, line_number)?;
        hunks.push(hunk);
        line_number += hunk_lines;
        remaining_lines = &remaining_lines[hunk_lines..];
    }
    let patch = patch_lines.join("\n");
    Ok(ApplyPatchArgs {
        hunks,
        patch,
        workdir: None,
    })
}

fn check_patch_boundaries_strict<'a>(
    lines: &'a [&'a str],
) -> Result<(&'a [&'a str], &'a [&'a str]), ParseError> {
    let (first_line, last_line) = match lines {
        [] => (None, None),
        [first] => (Some(first), Some(first)),
        [first, .., last] => (Some(first), Some(last)),
    };
    check_start_and_end_lines_strict(first_line, last_line)?;
    Ok((lines, &lines[1..lines.len() - 1]))
}

fn check_patch_boundaries_lenient<'a>(
    original_lines: &'a [&'a str],
) -> Result<(&'a [&'a str], &'a [&'a str]), ParseError> {
    let original_parse_error = match check_patch_boundaries_strict(original_lines) {
        Ok(lines) => return Ok(lines),
        Err(e) => e,
    };
    match original_lines {
        [first, .., last] => {
            if first.trim().starts_with("<<'EOF'") && last.trim() == "EOF" {
                let inner_lines = &original_lines[1..original_lines.len() - 1];
                check_patch_boundaries_strict(inner_lines)
            } else {
                Err(original_parse_error)
            }
        }
        _ => Err(original_parse_error),
    }
}

fn check_start_and_end_lines_strict(
    first_line: Option<&&str>,
    last_line: Option<&&str>,
) -> Result<(), ParseError> {
    let first_line = first_line.map(|line| line.trim());
    let last_line = last_line.map(|line| line.trim());
    match (first_line, last_line) {
        (Some(first), Some(last)) if first == BEGIN_PATCH_MARKER && last == END_PATCH_MARKER => Ok(()),
        (Some(first), _) if first != BEGIN_PATCH_MARKER => Err(InvalidPatchError(String::from(
            "The first line of the patch must be '*** Begin Patch'",
        ))),
        _ => Err(InvalidPatchError(String::from(
            "The last line of the patch must be '*** End Patch'",
        ))),
    }
}

fn parse_one_hunk(lines: &[&str], line_number: usize) -> Result<(Hunk, usize), ParseError> {
    let first_line = lines[0].trim();
    if let Some(path) = first_line.strip_prefix(ADD_FILE_MARKER) {
        let mut contents = String::new();
        let mut parsed_lines = 1;
        for add_line in &lines[1..] {
            if let Some(line_to_add) = add_line.strip_prefix('+') {
                contents.push_str(line_to_add);
                contents.push('\n');
                parsed_lines += 1;
            } else {
                break;
            }
        }
        return Ok((AddFile { path: PathBuf::from(path), contents }, parsed_lines));
    } else if let Some(path) = first_line.strip_prefix(DELETE_FILE_MARKER) {
        return Ok((DeleteFile { path: PathBuf::from(path) }, 1));
    } else if let Some(path) = first_line.strip_prefix(UPDATE_FILE_MARKER) {
        let mut remaining_lines = &lines[1..];
        let mut parsed_lines = 1;
        let move_path = remaining_lines.first().and_then(|x| x.strip_prefix(MOVE_TO_MARKER));
        if move_path.is_some() {
            remaining_lines = &remaining_lines[1..];
            parsed_lines += 1;
        }
        let mut chunks = Vec::new();
        while !remaining_lines.is_empty() {
            if remaining_lines[0].trim().is_empty() {
                parsed_lines += 1;
                remaining_lines = &remaining_lines[1..];
                continue;
            }
            if remaining_lines[0].starts_with('*') {
                break;
            }
            let (chunk, chunk_lines) =
                parse_update_file_chunk(remaining_lines, line_number + parsed_lines, chunks.is_empty())?;
            chunks.push(chunk);
            parsed_lines += chunk_lines;
            remaining_lines = &remaining_lines[chunk_lines..];
        }
        if chunks.is_empty() {
            return Err(InvalidHunkError {
                message: format!("Update file hunk for path '{}' is empty", Path::new(path).display()),
                line_number,
            });
        }
        return Ok((
            UpdateFile {
                path: PathBuf::from(path),
                move_path: move_path.map(PathBuf::from),
                chunks,
            },
            parsed_lines,
        ));
    }
    Err(InvalidHunkError {
        message: format!(
            "'{first_line}' is not a valid hunk header.\nValid hunk headers: '*** Add File: {{path}}', '*** Delete File: {{path}}', '*** Update File: {{path}}'"
        ),
        line_number,
    })
}

fn parse_update_file_chunk(
    lines: &[&str],
    line_number: usize,
    allow_missing_context: bool,
) -> Result<(UpdateFileChunk, usize), ParseError> {
    if lines.is_empty() {
        return Err(InvalidHunkError {
            message: "Update hunk does not contain any lines".to_string(),
            line_number,
        });
    }
    let (change_context, start_index) = if lines[0] == EMPTY_CHANGE_CONTEXT_MARKER {
        (None, 1)
    } else if let Some(context) = lines[0].strip_prefix(CHANGE_CONTEXT_MARKER) {
        (Some(context.to_string()), 1)
    } else {
        if !allow_missing_context {
            return Err(InvalidHunkError {
                message: format!(
                    "Expected update hunk to start with a @@ context marker, got: '{}'",
                    lines[0]
                ),
                line_number,
            });
        }
        (None, 0)
    };
    if start_index >= lines.len() {
        return Err(InvalidHunkError {
            message: "Update hunk does not contain any lines".to_string(),
            line_number: line_number + 1,
        });
    }
    let mut chunk = UpdateFileChunk {
        change_context,
        old_lines: Vec::new(),
        new_lines: Vec::new(),
        is_end_of_file: false,
    };
    let mut parsed_lines = 0;
    for line in &lines[start_index..] {
        match *line {
            EOF_MARKER => {
                if parsed_lines == 0 {
                    return Err(InvalidHunkError {
                        message: "Update hunk does not contain any lines".to_string(),
                        line_number: line_number + 1,
                    });
                }
                chunk.is_end_of_file = true;
                parsed_lines += 1;
                break;
            }
            line_contents => match line_contents.chars().next() {
                None => {
                    chunk.old_lines.push(String::new());
                    chunk.new_lines.push(String::new());
                }
                Some(' ') => {
                    chunk.old_lines.push(line_contents[1..].to_string());
                    chunk.new_lines.push(line_contents[1..].to_string());
                }
                Some('+') => {
                    chunk.new_lines.push(line_contents[1..].to_string());
                }
                Some('-') => {
                    chunk.old_lines.push(line_contents[1..].to_string());
                }
                _ => {
                    if parsed_lines == 0 {
                        return Err(InvalidHunkError {
                            message: format!(
                                "Unexpected line found in update hunk: '{line_contents}'.\nEvery line should start with ' ' (context line), '+' (added line), or '-' (removed line)"
                            ),
                            line_number: line_number + 1,
                        });
                    }
                    break;
                }
            },
        }
        parsed_lines += 1;
    }
    Ok((chunk, parsed_lines + start_index))
}
