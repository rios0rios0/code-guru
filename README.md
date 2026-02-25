<h1 align="center">Code Guru</h1>
<p align="center">
    <a href="https://github.com/rios0rios0/code-guru/releases/latest">
        <img src="https://img.shields.io/github/release/rios0rios0/code-guru.svg?style=for-the-badge&logo=github" alt="Latest Release"/></a>
    <a href="https://github.com/rios0rios0/code-guru/blob/main/LICENSE">
        <img src="https://img.shields.io/github/license/rios0rios0/code-guru.svg?style=for-the-badge&logo=github" alt="License"/></a>
    <a href="https://sonarcloud.io/summary/overall?id=rios0rios0_code-guru">
        <img src="https://img.shields.io/sonar/coverage/rios0rios0_code-guru?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarqubecloud" alt="Coverage"/></a>
    <a href="https://sonarcloud.io/summary/overall?id=rios0rios0_code-guru">
        <img src="https://img.shields.io/sonar/quality_gate/rios0rios0_code-guru?server=https%3A%2F%2Fsonarcloud.io&style=for-the-badge&logo=sonarqubecloud" alt="Quality Gate"/></a>
</p>

A Go tool that leverages the OpenAI API to automatically review code changes in GitLab merge requests and post helpful suggestions as discussion comments.

## Features

- Fetches open merge requests from a specified GitLab repository
- Reviews code diffs using OpenAI's GPT model
- Posts AI-generated review comments as merge request discussions
- Skips files with no detected issues

## Dependencies

- `github.com/xanzy/go-gitlab`
- `github.com/sashabaranov/go-openai`

## Installation

```bash
git clone https://github.com/rios0rios0/code-guru.git
cd code-guru
go get github.com/xanzy/go-gitlab
go get github.com/sashabaranov/go-openai
go build -o codeguru main.go
```

## Configuration

Set your GitLab API token and OpenAI API key as environment variables:

```bash
export GITLAB_API_TOKEN="your-gitlab-api-token"
export GITLAB_PROJECT_ID="your-gitlab-project-id"
export OPENAI_API_KEY="your-openai-api-key"
```

## Usage

Run the built executable:

```bash
./codeguru
```

The tool will fetch merge requests from the specified GitLab repository, review the code changes using the OpenAI API, and post the generated suggestions as comments on the merge requests.

**Notes:**
- You may need to adjust the OpenAI API call parameters for better results
- Handle API rate limits accordingly to prevent errors and ensure smooth operation

## Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

## License

See [LICENSE](LICENSE) for details.
