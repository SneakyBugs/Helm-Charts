from argparse import ArgumentParser
from pathlib import Path
from subprocess import run
from tempfile import TemporaryDirectory
from typing import Optional
import re
import sys


def clone(repo_url: str, location: Path):
    run(
        ["git", "clone", repo_url, location.as_posix()], check=True, capture_output=True
    )


def get_tags(repo: Path, contains: Optional[str]) -> list[str]:
    if contains:
        tag_result = run(
            ["git", "tag", "--contains", contains],
            cwd=repo.as_posix(),
            check=True,
            capture_output=True,
            text=True,
        )
        tags = tag_result.stdout.splitlines()
        tags.reverse()
        return tags
    tag_result = run(["git", "tag"], check=True, capture_output=True, text=True)
    tags = tag_result.stdout.splitlines()
    tags.reverse()
    return tags


def switch(repo: Path, tag: str):
    run(["git", "checkout", tag], cwd=repo.as_posix(), check=True)


def update_version(repo_path: Path, tag: str) -> str:
    run(
        [
            "python",
            Path("tools/release.py").absolute().as_posix(),
            tag,
            "--registry=dummy",
            "--no-push",
        ],
        check=True,
        cwd=repo_path.as_posix(),
    )


def frigate(chart_path: Path) -> str:
    frigate_result = run(
        ["frigate", "gen", chart_path.as_posix()], check=True, capture_output=True
    )
    return frigate_result.stdout.decode()


def parse_tag(tag: str) -> tuple[str, str]:
    parts = tag.split("-")

    if len(parts) < 2:
        print(
            f"Bad tag format of tag `{tag}`, must be <chart_name>-<chart_version>, for example `cluster-1.0.0`."
        )
        sys.exit(1)

    version = parts[-1]

    # Regex for checking semver compatibility is sourced from:
    # https://semver.org/#is-there-a-suggested-regular-expression-regex-to-check-a-semver-string
    if not re.match(
        "^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$",
        version,
    ):
        print(
            "Bad tag format, must be <chart_name>-<chart_version> and chart version must be semver compatible."
        )
        sys.exit(1)

    chart_name = "-".join(parts[:-1])
    return chart_name, version


def main() -> None:
    parser = ArgumentParser()
    parser.add_argument("repository", default=".", help="Git repository to build from")
    args = parser.parse_args()
    content_path = Path("docs/src/content/charts")

    if not content_path.is_dir():
        content_path.mkdir()

    with TemporaryDirectory() as temp_dir:
        temp_path = Path(temp_dir)
        repo_path = temp_path.joinpath("repo")
        clone(args.repository, repo_path)
        tags = get_tags(repo_path, None)
        parsed_tags = [[tag, *parse_tag(tag)] for tag in (tags)]

        for tag, chart, version in parsed_tags:
            print(tag, chart, version)
            switch(repo_path, tag)
            chart_path = repo_path.joinpath("charts").joinpath(chart)
            update_version(repo_path, tag)
            chart_doc = frigate(chart_path)

            chart_content_dir = content_path.joinpath(chart)
            if not chart_content_dir.is_dir():
                chart_content_dir.mkdir()

            doc_content_path = chart_content_dir.joinpath(f"{version}.md")
            with doc_content_path.open("w", encoding="utf-8") as doc_file:
                doc_file.write(chart_doc)

    # 1. Clone repo into tmp folder
    # 2. List tags
    # 3. For each tag:
    #    1. Switch to tag
    #    2. Run Frigate
    #    3. Copy generated README doc
    #    4. ? Reset?
    # 4. ???
    # 5. Profit


if __name__ == "__main__":
    main()
