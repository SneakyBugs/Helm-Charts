from argparse import ArgumentParser
import sys
import re
from pathlib import Path
import subprocess

import yaml


def main() -> None:
    parser = ArgumentParser()
    parser.add_argument("TAG", help="Git tag of chart release")
    parser.add_argument(
        "--registry",
        required=True,
        help="Helm OCI registry to push to, must start with oci://",
    )
    parser.add_argument("--no-push", action="store_true")
    args = parser.parse_args()

    tag: str = args.TAG
    no_push: bool = args.no_push

    parts = tag.split("-")

    if len(parts) < 2:
        print(
            "Bad tag format, must be <chart_name>-<chart_version>, for example `cluster-1.0.0`."
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

    chart_path = Path("charts").joinpath(chart_name)
    chart_config = chart_path.joinpath("Chart.yaml")
    if not chart_config.exists():
        print(f"File {chart_config} must exist.")
        sys.exit(1)

    print(
        f"Parsing config for chart `{chart_name}` and overriding `version: {version}`"
    )

    with chart_config.open(encoding="utf-8") as chart_config_file:
        parsed_chart_config = yaml.load(chart_config_file, yaml.SafeLoader)
        parsed_chart_config["version"] = version

    with chart_config.open(mode="w", encoding="utf-8") as chart_config_file:
        print("Writing Chart.yaml")
        yaml.dump(parsed_chart_config, chart_config_file)

    if no_push:
        print("Done! Skipping Helm package and push because of `--no-push` flag.")
        return

    helm_package_command = ["helm", "package", chart_path.as_posix()]
    print(f"Running `{' '.join(helm_package_command)}`")
    subprocess.run(helm_package_command, check=True)

    helm_push_command = ["helm", "push", f"{chart_name}-{version}.tgz", args.registry]
    print(f"Running `{' '.join(helm_push_command)}`")
    subprocess.run(helm_push_command, check=True)

    print(f"Shipped `{chart_name}` version {version} 🚀")


if __name__ == "__main__":
    main()
