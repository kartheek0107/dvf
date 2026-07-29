detect_system_info

  local repo_url="https://packages.gitlab.com/install/repositories/runner/gitlab-runner/${os}/${dist}/config_file.repo"
  local repo_dest

  if [ "$os" = "sles" ] || [ "$os" = "opensuse" ]; then
    repo_dest="/etc/zypp/repos.d/runner_gitlab-runner.repo"
  else
    repo_dest="/etc/yum.repos.d/runner_gitlab-runner.repo"
  fi

  fetch_and_install_repo "$repo_url" "$repo_dest"
  inject_credentials_to_repo_file "$repo_dest"

  if [ "$os" = "sles" ] || [ "$os" = "opensuse" ]; then
    refresh_zypper
    rpm_import_package_keys "$repo_dest"
  else
    install_optional_deps
    refresh_cache
  fi

  cat <<EOF
Repository installed successfully.
Packages are ready to install.
EOF
}

main
