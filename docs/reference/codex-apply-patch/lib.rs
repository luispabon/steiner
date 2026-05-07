mod invocation;
mod parser;
mod seek_sequence;
mod standalone_executable;
mod streaming_parser;

use std::collections::HashMap;
use std::io;
use std::path::Path;
use std::path::PathBuf;

use anyhow::Context;
use anyhow::Result;
use codex_exec_server::CreateDirectoryOptions;
use codex_exec_server::ExecutorFileSystem;
use codex_exec_server::FileSystemSandboxContext;
use codex_exec_server::RemoveOptions;
use codex_utils_absolute_path::AbsolutePathBuf;
use similar::TextDiff;
use thiserror::Error;

pub use invocation::maybe_parse_apply_patch_verified;
pub use parser::parse_patch;
pub use parser::Hunk;
pub use parser::ParseError;
pub use parser::UpdateFileChunk;
pub use standalone_executable::main;
pub use streaming_parser::StreamingPatchParser;

use crate::invocation::ExtractHeredocError;
use parser::ParseError::*;

pub const APPLY_PATCH_TOOL_INSTRUCTIONS: &str =
    include_str!("../apply_patch_tool_instructions.md");

pub const CODEX_CORE_APPLY_PATCH_ARG1: &str = "--codex-run-as-apply-patch";

#[derive(Debug, Error, PartialEq)]
pub enum ApplyPatchError {
    #[error(transparent)]
    ParseError(#[from] ParseError),
    #[error(transparent)]
    IoError(#[from] IoError),
    #[error("{0}")]
    ComputeReplacements(String),
    #[error("patch detected without explicit call to apply_patch.\nRerun as [\"apply_patch\", \"\"]")]
    ImplicitInvocation,
}

impl From<std::io::Error> for ApplyPatchError {
    fn from(err: std::io::Error) -> Self {
        ApplyPatchError::IoError(IoError {
            context: "I/O error".to_string(),
            source: err,
        })
    }
}

impl From<&std::io::Error> for ApplyPatchError {
    fn from(err: &std::io::Error) -> Self {
        ApplyPatchError::IoError(IoError {
            context: "I/O error".to_string(),
            source: std::io::Error::new(err.kind(), err.to_string()),
        })
    }
}

#[derive(Debug, Error)]
#[error("{context}: {source}")]
pub struct IoError {
    context: String,
    #[source]
    source: std::io::Error,
}

impl PartialEq for IoError {
    fn eq(&self, other: &Self) -> bool {
        self.context == other.context && self.source.to_string() == other.source.to_string()
    }
}

#[derive(Debug, PartialEq)]
pub struct ApplyPatchArgs {
    pub patch: String,
    pub hunks: Vec<Hunk>,
    pub workdir: Option<AbsolutePathBuf>,
}

#[derive(Debug, PartialEq)]
pub enum ApplyPatchFileChange {
    Add {
        content: String,
    },
    Delete {
        content: String,
    },
    Update {
        unified_diff: String,
        move_path: Option<PathBuf>,
        new_content: String,
    },
}

#[derive(Debug, PartialEq)]
pub enum MaybeApplyPatchVerified {
    Body(ApplyPatchAction),
    ShellParseError(ExtractHeredocError),
    CorrectnessError(ApplyPatchError),
    NotApplyPatch,
}

#[derive(Debug, PartialEq)]
pub struct ApplyPatchAction {
    changes: HashMap<PathBuf, ApplyPatchFileChange>,
    pub patch: String,
    pub cwd: AbsolutePathBuf,
}

impl ApplyPatchAction {
    pub fn is_empty(&self) -> bool {
        self.changes.is_empty()
    }

    pub fn changes(&self) -> &HashMap<PathBuf, ApplyPatchFileChange> {
        &self.changes
    }

    pub fn new_add_for_test(path: &AbsolutePathBuf, content: String) -> Self {
        #[expect(clippy::expect_used)]
        let filename = path.file_name().expect("path should not be empty").to_string_lossy();
        let patch = format!(
            r#"*** Begin Patch
*** Update File: {filename}
@@
{content}
*** End Patch"#,
        );
        let changes = HashMap::from([(path.to_path_buf(), ApplyPatchFileChange::Add { content })]);
        #[expect(clippy::expect_used)]
        Self {
            changes,
            cwd: path.parent().expect("path should have parent"),
            patch,
        }
    }
}

pub async fn apply_patch(
    patch: &str,
    cwd: &AbsolutePathBuf,
    stdout: &mut impl std::io::Write,
    stderr: &mut impl std::io::Write,
    fs: &dyn ExecutorFileSystem,
    sandbox: Option<&FileSystemSandboxContext>,
) -> Result<(), ApplyPatchError> {
    let hunks = match parse_patch(patch) {
        Ok(source) => source.hunks,
        Err(e) => {
            match &e {
                InvalidPatchError(message) => {
                    writeln!(stderr, "Invalid patch: {message}").map_err(ApplyPatchError::from)?;
                }
                InvalidHunkError {
                    message,
                    line_number,
                } => {
                    writeln!(stderr, "Invalid patch hunk on line {line_number}: {message}")
                        .map_err(ApplyPatchError::from)?;
                }
            }
            return Err(ApplyPatchError::ParseError(e));
        }
    };
    apply_hunks(&hunks, cwd, stdout, stderr, fs, sandbox).await?;
    Ok(())
}

pub async fn apply_hunks(
    hunks: &[Hunk],
    cwd: &AbsolutePathBuf,
    stdout: &mut impl std::io::Write,
    stderr: &mut impl std::io::Write,
    fs: &dyn ExecutorFileSystem,
    sandbox: Option<&FileSystemSandboxContext>,
) -> Result<(), ApplyPatchError> {
    match apply_hunks_to_files(hunks, cwd, fs, sandbox).await {
        Ok(affected) => {
            print_summary(&affected, stdout).map_err(ApplyPatchError::from)?;
            Ok(())
        }
        Err(err) => {
            let msg = err.to_string();
            writeln!(stderr, "{msg}").map_err(ApplyPatchError::from)?;
            if let Some(io) = err.downcast_ref::<io::Error>() {
                Err(ApplyPatchError::from(io))
            } else {
                Err(ApplyPatchError::IoError(IoError {
                    context: msg,
                    source: std::io::Error::other(err),
                }))
            }
        }
    }
}

pub struct AffectedPaths {
    pub added: Vec<PathBuf>,
    pub modified: Vec<PathBuf>,
    pub deleted: Vec<PathBuf>,
}

async fn apply_hunks_to_files(
    hunks: &[Hunk],
    cwd: &AbsolutePathBuf,
    fs: &dyn ExecutorFileSystem,
    sandbox: Option<&FileSystemSandboxContext>,
) -> anyhow::Result<AffectedPaths> {
    if hunks.is_empty() {
        anyhow::bail!("No files were modified.");
    }
    let mut added = Vec::new();
    let mut modified = Vec::new();
    let mut deleted = Vec::new();
    for hunk in hunks {
        let affected_path = hunk.path().to_path_buf();
        let path_abs = hunk.resolve_path(cwd);
        match hunk {
            Hunk::AddFile { contents, .. } => {
                write_file_with_missing_parent_retry(
                    fs,
                    &path_abs,
                    contents.clone().into_bytes(),
                    sandbox,
                )
                .await?;
                added.push(affected_path);
            }
            Hunk::DeleteFile { .. } => {
                let result: io::Result<()> = async {
                    let metadata = fs.get_metadata(&path_abs, sandbox).await?;
                    if metadata.is_directory {
                        return Err(io::Error::new(io::ErrorKind::InvalidInput, "path is a directory"));
                    }
                    fs.remove(
                        &path_abs,
                        RemoveOptions {
                            recursive: false,
                            force: false,
                        },
                        sandbox,
                    )
                    .await
                }
                .await;
                result.with_context(|| format!("Failed to delete file {}", path_abs.display()))?;
                deleted.push(affected_path);
            }
            Hunk::UpdateFile { move_path, chunks, .. } => {
                let AppliedPatch { new_contents, .. } =
                    derive_new_contents_from_chunks(&path_abs, chunks, fs, sandbox).await?;
                if let Some(dest) = move_path {
                    let dest_abs = AbsolutePathBuf::resolve_path_against_base(dest, cwd);
                    write_file_with_missing_parent_retry(
                        fs,
                        &dest_abs,
                        new_contents.into_bytes(),
                        sandbox,
                    )
                    .await?;
                    let result: io::Result<()> = async {
                        let metadata = fs.get_metadata(&path_abs, sandbox).await?;
                        if metadata.is_directory {
                            return Err(io::Error::new(
                                io::ErrorKind::InvalidInput,
                                "path is a directory",
                            ));
                        }
                        fs.remove(
                            &path_abs,
                            RemoveOptions {
                                recursive: false,
                                force: false,
                            },
                            sandbox,
                        )
                        .await
                    }
                    .await;
                    result.with_context(|| format!("Failed to remove original {}", path_abs.display()))?;
                    modified.push(affected_path);
                } else {
                    fs.write_file(&path_abs, new_contents.into_bytes(), sandbox)
                        .await
                        .with_context(|| format!("Failed to write file {}", path_abs.display()))?;
                    modified.push(affected_path);
                }
            }
        }
    }
    Ok(AffectedPaths { added, modified, deleted })
}

async fn write_file_with_missing_parent_retry(
    fs: &dyn ExecutorFileSystem,
    path_abs: &AbsolutePathBuf,
    contents: Vec<u8>,
    sandbox: Option<&FileSystemSandboxContext>,
) -> anyhow::Result<()> {
    match fs.write_file(path_abs, contents.clone(), sandbox).await {
        Ok(()) => Ok(()),
        Err(err) if err.kind() == io::ErrorKind::NotFound => {
            if let Some(parent_abs) = path_abs.parent() {
                fs.create_directory(
                    &parent_abs,
                    CreateDirectoryOptions { recursive: true },
                    sandbox,
                )
                .await
                .with_context(|| {
                    format!("Failed to create parent directories for {}", path_abs.display())
                })?;
            }
            fs.write_file(path_abs, contents, sandbox)
                .await
                .with_context(|| format!("Failed to write file {}", path_abs.display()))?;
            Ok(())
        }
        Err(err) => Err(err).with_context(|| format!("Failed to write file {}", path_abs.display())),
    }
}

struct AppliedPatch {
    original_contents: String,
    new_contents: String,
}

async fn derive_new_contents_from_chunks(
    path_abs: &AbsolutePathBuf,
    chunks: &[UpdateFileChunk],
    fs: &dyn ExecutorFileSystem,
    sandbox: Option<&FileSystemSandboxContext>,
) -> std::result::Result<AppliedPatch, ApplyPatchError> {
    let original_contents = fs.read_file_text(path_abs, sandbox).await.map_err(|err| {
        ApplyPatchError::IoError(IoError {
            context: format!("Failed to read file to update {}", path_abs.display()),
            source: err,
        })
    })?;
    let mut original_lines: Vec<String> = original_contents.split('\n').map(String::from).collect();
    if original_lines.last().is_some_and(String::is_empty) {
        original_lines.pop();
    }
    let replacements = compute_replacements(&original_lines, path_abs.as_path(), chunks)?;
    let mut new_lines = apply_replacements(original_lines, &replacements);
    if !new_lines.last().is_some_and(String::is_empty) {
        new_lines.push(String::new());
    }
    let new_contents = new_lines.join("\n");
    Ok(AppliedPatch {
        original_contents,
        new_contents,
    })
}

fn compute_replacements(
    original_lines: &[String],
    path: &Path,
    chunks: &[UpdateFileChunk],
) -> std::result::Result<Vec<(usize, usize, Vec<String>)>, ApplyPatchError> {
    let mut replacements: Vec<(usize, usize, Vec<String>)> = Vec::new();
    let mut line_index: usize = 0;
    for chunk in chunks {
        if let Some(ctx_line) = &chunk.change_context {
            if let Some(idx) = seek_sequence::seek_sequence(
                original_lines,
                std::slice::from_ref(ctx_line),
                line_index,
                false,
            ) {
                line_index = idx + 1;
            } else {
                return Err(ApplyPatchError::ComputeReplacements(format!(
                    "Failed to find context '{}' in {}",
                    ctx_line,
                    path.display()
                )));
            }
        }
        if chunk.old_lines.is_empty() {
            let insertion_idx = if original_lines.last().is_some_and(String::is_empty) {
                original_lines.len() - 1
            } else {
                original_lines.len()
            };
            replacements.push((insertion_idx, 0, chunk.new_lines.clone()));
            continue;
        }
        let mut pattern: &[String] = &chunk.old_lines;
        let mut found = seek_sequence::seek_sequence(original_lines, pattern, line_index, chunk.is_end_of_file);
        let mut new_slice: &[String] = &chunk.new_lines;
        if found.is_none() && pattern.last().is_some_and(String::is_empty) {
            pattern = &pattern[..pattern.len() - 1];
            if new_slice.last().is_some_and(String::is_empty) {
                new_slice = &new_slice[..new_slice.len() - 1];
            }
            found = seek_sequence::seek_sequence(
                original_lines,
                pattern,
                line_index,
                chunk.is_end_of_file,
            );
        }
        if let Some(start_idx) = found {
            replacements.push((start_idx, pattern.len(), new_slice.to_vec()));
            line_index = start_idx + pattern.len();
        } else {
            return Err(ApplyPatchError::ComputeReplacements(format!(
                "Failed to find expected lines in {}:\n{}",
                path.display(),
                chunk.old_lines.join("\n")
            )));
        }
    }
    replacements.sort_by(|(lhs_idx, _, _), (rhs_idx, _, _)| lhs_idx.cmp(rhs_idx));
    Ok(replacements)
}

fn apply_replacements(
    mut lines: Vec<String>,
    replacements: &[(usize, usize, Vec<String>)],
) -> Vec<String> {
    for (start_idx, old_len, new_segment) in replacements.iter().rev() {
        let start_idx = *start_idx;
        let old_len = *old_len;
        for _ in 0..old_len {
            if start_idx < lines.len() {
                lines.remove(start_idx);
            }
        }
        for (offset, new_line) in new_segment.iter().enumerate() {
            lines.insert(start_idx + offset, new_line.clone());
        }
    }
    lines
}

#[derive(Debug, Eq, PartialEq)]
pub struct ApplyPatchFileUpdate {
    unified_diff: String,
    content: String,
}

pub async fn unified_diff_from_chunks(
    path_abs: &AbsolutePathBuf,
    chunks: &[UpdateFileChunk],
    fs: &dyn ExecutorFileSystem,
    sandbox: Option<&FileSystemSandboxContext>,
) -> std::result::Result<ApplyPatchFileUpdate, ApplyPatchError> {
    unified_diff_from_chunks_with_context(path_abs, chunks, 1, fs, sandbox).await
}

pub async fn unified_diff_from_chunks_with_context(
    path_abs: &AbsolutePathBuf,
    chunks: &[UpdateFileChunk],
    context: usize,
    fs: &dyn ExecutorFileSystem,
    sandbox: Option<&FileSystemSandboxContext>,
) -> std::result::Result<ApplyPatchFileUpdate, ApplyPatchError> {
    let AppliedPatch {
        original_contents,
        new_contents,
    } = derive_new_contents_from_chunks(path_abs, chunks, fs, sandbox).await?;
    let text_diff = TextDiff::from_lines(&original_contents, &new_contents);
    let unified_diff = text_diff.unified_diff().context_radius(context).to_string();
    Ok(ApplyPatchFileUpdate {
        unified_diff,
        content: new_contents,
    })
}

pub fn print_summary(affected: &AffectedPaths, out: &mut impl std::io::Write) -> std::io::Result<()> {
    writeln!(out, "Success.")?;
    writeln!(out, "Updated the following files:")?;
    for path in &affected.added {
        writeln!(out, "A {}", path.display())?;
    }
    for path in &affected.modified {
        writeln!(out, "M {}", path.display())?;
    }
    for path in &affected.deleted {
        writeln!(out, "D {}", path.display())?;
    }
    Ok(())
}
