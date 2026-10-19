# A custom tool of the crew's program, loaded by name from the agent's
# declaration when the crew runs. The import never reads it.
from crewai.tools import BaseTool


class ChangelogReader(BaseTool):
    name: str = "changelog_reader"
    description: str = "Reads the changelog of the repository."

    def _run(self) -> str:
        raise NotImplementedError("the program brings its own reader")
