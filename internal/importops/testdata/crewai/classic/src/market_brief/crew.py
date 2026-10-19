# The crew's program, which the import never reads. A test makes this file
# unreadable and imports the project directory all the same.
from crewai import Agent, Crew, Process, Task
from crewai.project import CrewBase, agent, crew, task, tool


@CrewBase
class MarketBrief:
    agents_config = "config/agents.yaml"
    tasks_config = "config/tasks.yaml"

    @tool
    def search_tool(self):
        raise NotImplementedError("the program brings its own search tool")

    @agent
    def researcher(self) -> Agent:
        return Agent(config=self.agents_config["researcher"])

    @agent
    def brief_writer(self) -> Agent:
        return Agent(config=self.agents_config["brief_writer"])

    @task
    def research_task(self) -> Task:
        return Task(config=self.tasks_config["research_task"])

    @task
    def brief_task(self) -> Task:
        return Task(config=self.tasks_config["brief_task"])

    @crew
    def crew(self) -> Crew:
        return Crew(agents=self.agents, tasks=self.tasks, process=Process.sequential)
